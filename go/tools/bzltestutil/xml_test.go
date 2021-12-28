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
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// jsonEvent as encoded by the test2json package.
type jsonEvent struct {
	Time    *time.Time
	Action  string
	Package string
	Test    string
	Elapsed *float64
	Output  string
}

func TestJSON2XML(t *testing.T) {
	files, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		t.Run(name, func(t *testing.T) {
			orig, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(orig)
			var events []event
			for {
				var e jsonEvent
				if err := dec.Decode(&e); err == io.EOF {
					break
				} else if err != nil {
					t.Errorf("error decoding test2json output: %s", err)
				}
				events = append(events, event{
					e.Time,
					e.Action,
					e.Package,
					e.Test,
					e.Elapsed,
					e.Output,
				})
			}
			got, err := events2xml(events, "pkg/testing")
			if err != nil {
				t.Fatal(err)
			}

			target := strings.TrimSuffix(file, ".json") + ".xml"
			want, err := ioutil.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(got, want) {
				t.Errorf("events2xml for %s does not match, got:\n%s\nwant:\n%s\n", name, string(got), string(want))
			}
		})
	}
}
