package utils

func MapEnsureAndSet[K comparable, V any](m *map[K]V, key K, value V) {
	if m == nil {
		panic("can not add to nil")
	}
	if *m == nil {
		*m = make(map[K]V, 1)
	}
	(*m)[key] = value
}

func MapDiff[K comparable, V any](left map[K]V, right map[K]V) (inLeft map[K]V, inBoth map[K]V, inRight map[K]V) {
	inLeft, inBoth, inRight = make(map[K]V), make(map[K]V), make(map[K]V)
	for k, v := range left {
		if _, ok := right[k]; !ok {
			inLeft[k] = v
		} else {
			inBoth[k] = v
		}
	}

	for k, v := range right {
		if _, ok := inBoth[k]; !ok {
			inRight[k] = v
		}
	}

	return
}
