package maps

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
