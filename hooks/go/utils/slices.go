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

// TODO: rename package to something meaningful (revive: var-naming).
package utils //nolint:revive

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
