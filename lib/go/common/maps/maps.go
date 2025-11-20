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
