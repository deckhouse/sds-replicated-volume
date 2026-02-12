package testing

import "testing"

type TestFunction func(*testing.T)

type Zymbol[T any] struct {
	Provider func(*testing.T) T
}

type Symbol[T any] struct {
	providers map[*testing.T]TestFunction
	values    map[*testing.T]T
}

func (s *Symbol[T]) SetProvider(t *testing.T, tf TestFunction) {
	t.Helper()

	if _, ok := s.providers[t]; ok {
		t.Fatal("provider is already set", s)
	}

	if s.providers == nil {
		s.providers = make(map[*testing.T]TestFunction, 1)
	}
	s.providers[t] = tf

	t.Cleanup(func() { delete(s.providers, t) })
}

func (s *Symbol[T]) Require(t *testing.T) (val T) {
	if alreadyProvidedVal, ok := s.values[t]; ok {
		return alreadyProvidedVal
	}

	tf, ok := s.providers[t]
	if !ok {
		t.Fatal("provider not set")
	}

	tf(t)

	if justProvidedVal, ok := s.values[t]; ok {
		return justProvidedVal
	}

	t.Fatal("provider didn't provide the value")

	return
}

func (s *Symbol[T]) Provide(t *testing.T, val T) {
	t.Helper()

	if _, ok := s.values[t]; ok {
		t.Fatal("symbol is already provided", s)
	}

	if s.values == nil {
		s.values = make(map[*testing.T]T, 1)
	}
	s.values[t] = val

	t.Cleanup(func() { delete(s.values, t) })
}
