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
