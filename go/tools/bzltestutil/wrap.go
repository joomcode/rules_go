// Copyright 2020 The Bazel Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bzltestutil

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// TestWrapperAbnormalExit is used by Wrap to indicate the child
// process exitted without an exit code (for example being killed by a signal).
// We use 6, in line with Bazel's RUN_FAILURE.
const TestWrapperAbnormalExit = 6

func ShouldWrap() bool {
	if wrapEnv, ok := os.LookupEnv("GO_TEST_WRAP"); ok {
		wrap, err := strconv.ParseBool(wrapEnv)
		if err != nil {
			log.Fatalf("invalid value for GO_TEST_WRAP: %q", wrapEnv)
		}
		return wrap
	}
	_, ok := os.LookupEnv("XML_OUTPUT_FILE")
	return ok
}

// shouldAddTestV indicates if the test wrapper should prepend a -test.v flag to
// the test args. This is required to get information about passing tests from
// test2json for complete XML reports.
func shouldAddTestV() bool {
	if wrapEnv, ok := os.LookupEnv("GO_TEST_WRAP_TESTV"); ok {
		wrap, err := strconv.ParseBool(wrapEnv)
		if err != nil {
			log.Fatalf("invalid value for GO_TEST_WRAP_TESTV: %q", wrapEnv)
		}
		return wrap
	}
	return false
}

func Wrap(pkg string) error {
	testOutputConverter := NewMixedConverter(pkg, Timestamp)

	args := os.Args[1:]
	if shouldAddTestV() {
		args = append([]string{"-test.v"}, args...)
	}
	exePath := os.Args[0]
	if !filepath.IsAbs(exePath) && strings.ContainsRune(exePath, filepath.Separator) && testExecDir != "" {
		exePath = filepath.Join(testExecDir, exePath)
	}
	cmd := exec.Command(exePath, args...)
	cmd.Env = append(os.Environ(), "GO_TEST_WRAP=0")
	cmd.Stderr = io.MultiWriter(os.Stderr, testOutputConverter.stderrConverter)
	cmd.Stdout = io.MultiWriter(os.Stdout, testOutputConverter.stdoutConverter)

	var err error
	processResult := make(chan error, 1)
	timeout := false

	cancelChan := make(chan os.Signal, 1)
	signal.Notify(cancelChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		processResult <- cmd.Run()
	}()
	select {
	case err = <-processResult:
	case <-cancelChan:
		timeout = true
		err = cmd.Process.Signal(os.Interrupt)
	}
	testOutputConverter.Close(timeout)

	if out, ok := os.LookupEnv("XML_OUTPUT_FILE"); ok {
		werr := writeReport(testOutputConverter.GetOutput(), pkg, out)
		if werr != nil {
			if err != nil {
				return fmt.Errorf("error while generating testreport: %s, (error wrapping test execution: %s)", werr, err)
			}
			return fmt.Errorf("error while generating testreport: %s", werr)
		}
	}
	return err
}

func writeReport(events []event, pkg string, path string) error {
	xml, cerr := events2xml(events, pkg)
	if cerr != nil {
		return fmt.Errorf("error converting test output to xml: %s", cerr)
	}
	if err := ioutil.WriteFile(path, xml, 0664); err != nil {
		return fmt.Errorf("error writing test xml: %s", err)
	}
	return nil
}
