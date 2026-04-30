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

package suite

import "strings"

// TestID is the stable test identifier used in resource names.
// Deterministic by default so that failed cleanup causes name conflicts
// on the next run.
type TestID string

// ResourceName produces a deterministic resource name by joining the test ID
// with the provided identifiers: "{testId}-{ids joined with -}".
func (id TestID) ResourceName(ids ...string) string {
	return string(id) + "-" + strings.Join(ids, "-")
}
