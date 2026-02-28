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

package sync

import "context"

// Wait reads values from ch until predicate returns true, then returns the
// matching value and true. If ctx is cancelled or the channel closes before a
// match, returns the zero value of T and false.
func Wait[T any](ctx context.Context, ch <-chan T, predicate func(T) bool) (T, bool) {
	for {
		select {
		case <-ctx.Done():
			var zero T
			return zero, false
		case v, ok := <-ch:
			if !ok {
				var zero T
				return zero, false
			}
			if predicate(v) {
				return v, true
			}
		}
	}
}
