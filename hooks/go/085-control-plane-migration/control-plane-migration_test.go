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

package controlplanemigration

import "testing"

func TestIsDevDeckhouseVersion(t *testing.T) {
	cases := map[string]bool{
		"dev":             true,
		"dev-1.72":        true,
		"v1.72.0-dev":     true,
		"1.72.0":          false,
		"v1.72.0":         false,
		"v1.72.0-alpha.1": false,
		"v1.72.0-beta.2":  false,
		"":                false,
	}

	for version, want := range cases {
		if got := isDevDeckhouseVersion(version); got != want {
			t.Errorf("isDevDeckhouseVersion(%q) = %v, want %v", version, got, want)
		}
	}
}
