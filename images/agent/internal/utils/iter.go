package utils

import (
	"iter"
)

func IterToKeys[K comparable](s iter.Seq[K]) iter.Seq2[K, struct{}] {
	return func(yield func(K, struct{}) bool) {
		for k := range s {
			if !yield(k, struct{}{}) {
				return
			}
		}
	}
}

func IterMap[T any, U any](src iter.Seq[T], f func(T) U) iter.Seq[U] {
	return func(yield func(U) bool) {
		for v := range src {
			if !yield(f(v)) {
				return
			}
		}
	}
}

func IterFilter[T any](s []T, p func(v T) bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, v := range s {
			if !p(v) {
				continue
			}
			if !yield(v) {
				return
			}
		}
	}
}
