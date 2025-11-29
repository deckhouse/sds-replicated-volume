/*
Copyright 2025 Flant JSC

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

package maps

import (
	"fmt"

	"golang.org/x/exp/constraints"
)

func SetUnique[K comparable, V any](m map[K]V, key K, value V) (map[K]V, bool) {
	if m == nil {
		return map[K]V{key: value}, true
	}
	if _, ok := m[key]; !ok {
		m[key] = value
		return m, true
	}

	return m, false
}

func SetLowestUnused[T constraints.Integer](used map[T]struct{}, minVal, maxVal T) (map[T]struct{}, T, error) {
	for v := minVal; v <= maxVal; v++ {
		if usedUpd, added := SetUnique(used, v, struct{}{}); added {
			return usedUpd, v, nil
		}
	}
	return used, 0, fmt.Errorf("unable to find unused number in range [%d;%d]", minVal, maxVal)
}
