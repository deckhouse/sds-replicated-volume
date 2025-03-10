package utils

import (
	"iter"
)

type Pair[K comparable, V any] struct {
	Key   K
	Value V
}

func IterPair[K comparable, V any](seq iter.Seq2[K, V]) iter.Seq[Pair[K, V]] {
	return func(yield func(Pair[K, V]) bool) {
		for k, v := range seq {
			if !yield(Pair[K, V]{k, v}) {
				return
			}
		}
	}
}

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

func IterZip[T any, U any](left iter.Seq[T], right iter.Seq[U]) iter.Seq2[T, U] {
	return func(yield func(T, U) bool) {
		nextLeft, stopLeft := iter.Pull(left)
		defer stopLeft()

		nextRight, stopRight := iter.Pull(right)
		defer stopRight()

		for {
			leftVal, leftOk := nextLeft()
			rightVal, rightOk := nextRight()

			// if either sequence is exhausted, stop
			if !leftOk || !rightOk {
				return
			}

			// Yield the pair
			if !yield(leftVal, rightVal) {
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
