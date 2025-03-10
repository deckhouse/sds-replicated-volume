package utils

import "iter"

func SliceFind[T any](s []T, f func(v *T) bool) *T {
	for i := range s {
		if f(&s[i]) {
			return &s[i]
		}
	}
	return nil
}

func SliceFilter[T any](s []T, p func(v *T) bool) iter.Seq[*T] {
	return func(yield func(*T) bool) {
		for i := range s {
			if !p(&s[i]) {
				continue
			}
			if !yield(&s[i]) {
				return
			}
		}
	}
}

func SliceMap[T any, U any](s []T, f func(v *T) U) iter.Seq[U] {
	return func(yield func(U) bool) {
		for i := range s {
			if !yield(f(&s[i])) {
				return
			}
		}
	}
}

func SliceIndex[K comparable, V any](s []V, indexFn func(v *V) K) iter.Seq2[K, *V] {
	return func(yield func(K, *V) bool) {
		for i := range s {
			k := indexFn(&s[i])
			if !yield(k, &s[i]) {
				return
			}

		}
	}
}
