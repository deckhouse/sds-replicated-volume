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
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

const (
	defaultSpecTimeout     = 30 * time.Second
	defaultSlowSpecTimeout = 1 * time.Minute
)

// parseTimeoutMultiplier parses a timeout multiplier value from a string.
// Returns 1.0 for empty, invalid, zero, or negative values.
func parseTimeoutMultiplier(s string) float64 {
	if s == "" {
		return 1.0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 1.0
	}
	return v
}

var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

// hasContextBody reports whether args contain a function whose first
// parameter implements context.Context (covers both context.Context and
// SpecContext).
func hasContextBody(args []any) bool {
	for _, arg := range args {
		t := reflect.TypeOf(arg)
		if t == nil || t.Kind() != reflect.Func || t.NumIn() == 0 {
			continue
		}
		if t.In(0).Implements(contextType) {
			return true
		}
	}
	return false
}

// collectNodeLabels returns the union of labels from parent containers
// (via CurrentTreeConstructionNodeReport) and labels present in args.
func collectNodeLabels(args []any) []string {
	seen := map[string]bool{}
	var all []string
	for _, label := range CurrentTreeConstructionNodeReport().Labels() {
		if !seen[label] {
			seen[label] = true
			all = append(all, label)
		}
	}
	for _, arg := range args {
		if labels, ok := arg.(Labels); ok {
			for _, label := range labels {
				if !seen[label] {
					seen[label] = true
					all = append(all, label)
				}
			}
		}
	}
	return all
}

func hasLabel(labels []string, target string) bool {
	return slices.Contains(labels, target)
}

// registerTimeoutPolicy registers a NodeArgsTransformer that enforces
// the SpecTimeout policy:
//   - Default 30s for all context-accepting It nodes.
//   - With the Slow label, default is 1min; explicit values > 30s are allowed.
//   - Without Slow, explicit SpecTimeout > 30s is an error.
//   - All final values are scaled by the given multiplier.
func registerTimeoutPolicy(multiplier float64) {
	AddTreeConstructionNodeArgsTransformer(
		func(nodeType types.NodeType, _ Offset, text string, args []any) (string, []any, []error) {
			if !nodeType.Is(types.NodeTypeIt) {
				return text, args, nil
			}
			if !hasContextBody(args) {
				return text, args, nil
			}

			labels := collectNodeLabels(args)
			isSlow := hasLabel(labels, LabelSlow)

			var authored time.Duration
			hasExplicit := false
			args = slices.DeleteFunc(args, func(arg any) bool {
				if st, ok := arg.(SpecTimeout); ok {
					authored = time.Duration(st)
					hasExplicit = true
					return true
				}
				return false
			})

			if hasExplicit && authored > defaultSpecTimeout && !isSlow {
				return text, args, []error{
					fmt.Errorf(
						"SpecTimeout(%s) on %q exceeds %s limit; add Label(%q) or reduce the timeout",
						authored, text, defaultSpecTimeout, LabelSlow,
					),
				}
			}

			base := defaultSpecTimeout
			if isSlow {
				base = defaultSlowSpecTimeout
			}
			if hasExplicit {
				base = authored
			}

			final := time.Duration(float64(base) * multiplier)
			args = append(args, SpecTimeout(final))

			return text, args, nil
		},
	)
}
