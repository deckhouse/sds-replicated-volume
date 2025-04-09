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
