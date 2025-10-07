package topology

import "iter"

//
// iter
//

func repeat[T any](src []T, counts []int) iter.Seq[T] {
	if len(src) != len(counts) {
		panic("expected len(src) == len(counts)")
	}

	return func(yield func(T) bool) {
		for i := 0; i < len(src); i++ {
			for range counts[i] {
				if !yield(src[i]) {
					return
				}
			}
		}
	}
}

// opposite of [repeat]
func compact[T any](src []T, counts []int) [][]T {
	res := make([][]T, len(counts))

	var srcIndex int
	for i, count := range counts {
		for range count {
			if srcIndex == len(src) {
				panic("expected len(src) to be sum of all counts, got smaller")
			}
			res[i] = append(res[i], src[srcIndex])
			srcIndex++
		}
	}
	if srcIndex != len(src) {
		panic("expected len(src) to be sum of all counts, got bigger")
	}
	return res
}

//
// combinations
//

func elementCombinations[T any](s []T, k int) iter.Seq[[]T] {
	result := make([]T, k)

	return func(yield func([]T) bool) {
		for sIndexes := range indexCombinations(len(s), k) {
			for i, sIndex := range sIndexes {
				result[i] = s[sIndex]
			}

			if !yield(result) {
				return
			}
		}
	}
}

// indexCombinations yields all k-combinations of indices [0..n).
// The same backing slice is reused for every yield.
// If you need to retain a combination, copy it in the caller.
func indexCombinations(n int, k int) iter.Seq[[]int] {
	if k > n {
		panic("expected k<=n")
	}

	result := make([]int, k)

	return func(yield func([]int) bool) {
		if k == 0 {
			return
		}

		// Initialize to the first combination: [0,1,2,...,k-1]
		for i := range k {
			result[i] = i
		}
		if !yield(result) {
			return
		}

		resultTail := k - 1
		nk := n - k

		for {
			// find rightmost index that can be incremented
			i := resultTail

			for {
				if result[i] == nk+i {
					// already maximum
					i--
				} else {
					// found
					break
				}

				if i < 0 {
					// all combinations generated
					return
				}
			}

			// increment and reset the tail to the minimal increasing sequence.
			result[i]++
			next := result[i]
			for j := i + 1; j < k; j++ {
				next++
				result[j] = next
			}

			if !yield(result) {
				return
			}
		}
	}
}
