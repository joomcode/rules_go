/* Copyright 2018 The Bazel Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Loads and runs registered analyses on a well-typed Go package.
// The code in this file is combined with the code generated by
// generate_checker_main.go.

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/bazelbuild/rules_go/go/tools/analysis"
	"golang.org/x/tools/go/gcexportdata"
)

// run returns an error only if the package is successfully loaded and at least
// one analysis fails. All other errors (e.g. during loading) are logged but
// do not return an error so as not to unnecessarily interrupt builds.
func run(args []string) error {
	srcs := multiFlag{}
	stdImports := multiFlag{}
	flags := flag.NewFlagSet("checker", flag.ContinueOnError)
	flags.Var(&srcs, "src", "A source file being compiled")
	flags.Var(&stdImports, "stdimport", "A standard library import path")
	vetTool := flags.String("vet_tool", "", "The vet tool")
	importcfg := flags.String("importcfg", "", "The import configuration file")
	if err := flags.Parse(args); err != nil {
		log.Println(err)
		return nil
	}
	packageFile, importMap, err := readImportCfg(*importcfg)
	if err != nil {
		return fmt.Errorf("error parsing importcfg: %v", err)
	}
	if enableVet {
		vcfgFile, err := buildVetcfgFile(packageFile, importMap, stdImports, srcs)
		if err != nil {
			log.Printf("error creating vet config: %v", err)
			return nil
		}
		defer os.Remove(vcfgFile)
		findings, err := runVet(*vetTool, vcfgFile)
		if err != nil {
			return fmt.Errorf("error running vet:\n%v\n", err)
		} else if findings != "" {
			return fmt.Errorf("errors found by vet:\n%s\n", findings)
		}
	}
	c := make(chan result)
	fset := token.NewFileSet()
	if err := runAnalyses(c, fset, packageFile, importMap, flags.Args()); err != nil {
		log.Printf("error running analyses: %s\n", err)
		return nil
	}
	if err := checkAnalysisResults(c, fset); err != nil {
		return fmt.Errorf("errors found during build-time code analysis:\n%s\n", err)
	}
	return nil
}

// Adapted from go/src/cmd/compile/internal/gc/main.go. Keep in sync.
func readImportCfg(file string) (packageFile map[string]string, importMap map[string]string, err error) {
	packageFile, importMap = make(map[string]string), make(map[string]string)
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, nil, fmt.Errorf("-importcfg: %v", err)
	}

	for lineNum, line := range strings.Split(string(data), "\n") {
		lineNum++ // 1-based
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var verb, args string
		if i := strings.Index(line, " "); i < 0 {
			verb = line
		} else {
			verb, args = line[:i], strings.TrimSpace(line[i+1:])
		}
		var before, after string
		if i := strings.Index(args, "="); i >= 0 {
			before, after = args[:i], args[i+1:]
		}
		switch verb {
		default:
			log.Fatalf("%s:%d: unknown directive %q", file, lineNum, verb)
		case "importmap":
			if before == "" || after == "" {
				return nil, nil, fmt.Errorf(`%s:%d: invalid importmap: syntax is "importmap old=new"`, file, lineNum)
			}
			importMap[before] = after
		case "packagefile":
			if before == "" || after == "" {
				return nil, nil, fmt.Errorf(`%s:%d: invalid packagefile: syntax is "packagefile path=filename"`, file, lineNum)
			}
			packageFile[before] = after
		}
	}
	return
}

// runAnalyses runs all analyses, filters results, and writes findings to the
// given channel.
func runAnalyses(c chan result, fset *token.FileSet, packageFile, importMap map[string]string, srcFiles []string) error {
	if len(analysis.Analyses()) == 0 {
		return nil
	}
	apkg, err := newAnalysisPackage(fset, packageFile, importMap, srcFiles)
	if err != nil {
		return fmt.Errorf("error building analysis package: %s\n", err)
	}
	// Run all other analyses.
	for _, a := range analysis.Analyses() {
		go func(a *analysis.Analysis) {
			defer func() {
				// Prevent a panic in a single analysis from interrupting other analyses.
				if r := recover(); r != nil {
					c <- result{name: a.Name, err: fmt.Errorf("panic : %v", r)}
				}
			}()
			res, err := a.Run(apkg)
			switch err {
			case nil:
				c <- result{analysis.Result{res.Findings}, a.Name, nil}
			case analysis.ErrSkipped:
				c <- result{name: a.Name, err: fmt.Errorf("skipped : %v", err)}
			default:
				c <- result{name: a.Name, err: fmt.Errorf("internal error: %v", err)}
			}
		}(a)
	}
	return nil
}

func newAnalysisPackage(fset *token.FileSet, packageFile, importMap map[string]string, srcFiles []string) (*analysis.Package, error) {
	imp := &importer{
		fset:         fset,
		importMap:    importMap,
		packageCache: make(map[string]*types.Package),
		packageFile:  packageFile,
	}
	apkg, err := load(fset, imp, srcFiles)
	if err != nil {
		return nil, fmt.Errorf("error loading package: %v\n", err)
	}
	return apkg, nil
}

// load parses and type checks the source code in each file in filenames.
func load(fset *token.FileSet, imp types.Importer, filenames []string) (*analysis.Package, error) {
	if len(filenames) == 0 {
		return nil, errors.New("no filenames")
	}
	var files []*ast.File
	for _, file := range filenames {
		f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	config := types.Config{Importer: imp}
	info := &types.Info{
		Types:     make(map[ast.Expr]types.TypeAndValue),
		Uses:      make(map[*ast.Ident]types.Object),
		Defs:      make(map[*ast.Ident]types.Object),
		Implicits: make(map[ast.Node]types.Object),
	}
	pkg, err := config.Check(files[0].Name.Name, fset, files, info)
	if err != nil {
		return nil, errors.New("type-checking failed")
	}
	return &analysis.Package{Fset: fset, Files: files, Types: pkg, Info: info}, nil
}

// checkAnalysisResults checks the analysis results written to the given channel
// and returns an error if the analysis finds errors that should fail
// compilation.
func checkAnalysisResults(c chan result, fset *token.FileSet) error {
	var analysisFindings []*analysis.Finding
	for i := 0; i < len(analysis.Analyses()); i++ {
		result := <-c
		if result.err != nil {
			// Analysis failed or skipped.
			log.Printf("analysis %q: %v", result.name, result.err)
			continue
		}
		if len(result.Findings) == 0 {
			continue
		}
		config, ok := configs[result.name]
		if !ok {
			// If the check is not explicitly configured, it applies to all files.
			analysisFindings = append(analysisFindings, result.Findings...)
			continue
		}
		// Discard findings based on the check configuration.
		for _, finding := range result.Findings {
			filename := fset.File(finding.Pos).Name()
			include := true
			if len(config.applyTo) > 0 {
				// This analysis applies exclusively to a set of files.
				include = false
				for pattern := range config.applyTo {
					if matched, err := regexp.MatchString(pattern, filename); err == nil && matched {
						include = true
					}
				}
			}
			for pattern := range config.whitelist {
				if matched, err := regexp.MatchString(pattern, filename); err == nil && matched {
					include = false
				}
			}
			if include {
				analysisFindings = append(analysisFindings, finding)
			}
		}
	}
	if len(analysisFindings) == 0 {
		return nil
	}
	sort.Slice(analysisFindings, func(i, j int) bool {
		return analysisFindings[i].Pos < analysisFindings[j].Pos
	})
	var errMsg bytes.Buffer
	for i, f := range analysisFindings {
		if i > 0 {
			errMsg.WriteByte('\n')
		}
		errMsg.WriteString(fmt.Sprintf("%s: %s\n", fset.Position(f.Pos), f.Message))
	}
	return errors.New(errMsg.String())
}

type config struct {
	applyTo, whitelist map[string]bool
}

func main() {
	log.SetFlags(0) // no timestamp
	log.SetPrefix("GoChecker: ")
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

// result is used to collate all the findings and errors returned
// by analyses run in parallel.
type result struct {
	analysis.Result
	name string
	err  error
}

type importer struct {
	fset         *token.FileSet
	importMap    map[string]string         // map import path in source code to package path
	packageCache map[string]*types.Package // cache of previously imported packages
	packageFile  map[string]string         // map package path to .a file with export data
}

func (i *importer) Import(path string) (*types.Package, error) {
	if imp, ok := i.importMap[path]; ok {
		// Translate import path if necessary.
		path = imp
	}
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if pkg, ok := i.packageCache[path]; ok && pkg.Complete() {
		return pkg, nil // cache hit
	}

	archive, ok := i.packageFile[path]
	if !ok {
		return nil, fmt.Errorf("could not import %q", path)
	}
	// open file
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer func() {
		f.Close()
		if err != nil {
			// add file name to error
			err = fmt.Errorf("reading export data: %s: %v", archive, err)
		}
	}()

	r, err := gcexportdata.NewReader(f)
	if err != nil {
		return nil, err
	}

	return gcexportdata.Read(r, i.fset, i.packageCache, path)
}
