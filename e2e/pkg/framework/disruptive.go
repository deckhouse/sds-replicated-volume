/*
Copyright 2026 Flant JSC

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

package framework

import (
	"math"
	"os"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

// registerDisruptiveTransformer registers a NodeArgsTransformer that
// detects the Disruptive label and auto-injects Serial (run on process #1
// after all other workers exit) and SpecPriority(-1) (run last among
// serial specs).
func registerDisruptiveTransformer() {
	AddTreeConstructionNodeArgsTransformer(
		func(nodeType types.NodeType, _ Offset, text string, args []any) (string, []any, []error) {
			if !nodeType.Is(types.NodeTypesForContainerAndIt) {
				return text, args, nil
			}
			if !hasDisruptiveLabel(args) {
				return text, args, nil
			}
			args = append(args, Serial, SpecPriority(math.MinInt))
			return text, args, nil
		},
	)
}

// hasDisruptiveLabel reports whether args contain a Labels value with
// the Disruptive entry.
func hasDisruptiveLabel(args []any) bool {
	for _, arg := range args {
		if labels, ok := arg.(Labels); ok {
			if slices.Contains(labels, LabelDisruptive) {
				return true
			}
		}
	}
	return false
}

// enforceDisruptive skips the current spec if it carries the Disruptive
// label and the E2E_ALLOW_DISRUPTIVE environment variable is not set.
func enforceDisruptive() {
	if !slices.Contains(CurrentSpecReport().Labels(), LabelDisruptive) {
		return
	}
	if os.Getenv("E2E_ALLOW_DISRUPTIVE") == "" {
		Skip("disruptive tests require E2E_ALLOW_DISRUPTIVE=true")
	}
}
