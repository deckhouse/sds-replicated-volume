/*
Copyright 2026 Flant JSC

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

package maps

import (
	"fmt"
	"iter"
	"maps"

	"golang.org/x/exp/constraints"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
)

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

func SetLowestUnused[T constraints.Integer](used map[T]struct{}, minVal, maxVal T) (map[T]struct{}, T, error) {
	if minVal > maxVal {
		return used, 0, fmt.Errorf("invalid range: minVal (%d) > maxVal (%d)", minVal, maxVal)
	}
	for v := minVal; v <= maxVal; v++ {
		if usedUpd, added := SetUnique(used, v, struct{}{}); added {
			return usedUpd, v, nil
		}
	}
	return used, 0, fmt.Errorf("unable to find unused number in range [%d;%d]", minVal, maxVal)
}

type ValuePair[VL any, VR any] struct {
	Left  VL
	Right VR
}

// Intersect computes the relationship between the keys of two maps.
// It returns three maps:
//   - onlyLeft: keys present in left but not in right, with left values
//   - both: keys present in both left and right, with paired values
//   - onlyRight: keys present in right but not in left, with right values
func Intersect[K comparable, VL any, VR any](
	left map[K]VL,
	right map[K]VR,
) (
	onlyLeft map[K]VL,
	both map[K]ValuePair[VL, VR],
	onlyRight map[K]VR,
) {
	return intersect(maps.All(left), right, len(left)+len(right))
}

// IntersectKeys is like [Intersect] but treats maps as sets (ignores values).
func IntersectKeys[K comparable, VL any, VR any](
	left map[K]VL,
	right map[K]VR,
) (
	onlyLeft map[K]struct{},
	both map[K]struct{},
	onlyRight map[K]struct{},
) {
	return intersectKeys(maps.Keys(left), right, len(left)+len(right))
}

// IntersectIters is like [Intersect] but for key-only iterators.
func IntersectIters[K comparable](
	left iter.Seq[K],
	right iter.Seq[K],
) (
	onlyLeft map[K]struct{},
	both map[K]struct{},
	onlyRight map[K]struct{},
) {
	rightMap := maps.Collect(uiter.MapTo2(right, asKey[K]))
	return intersectKeys(left, rightMap, len(rightMap))
}

// IntersectIters2 is like [Intersect] but for key-value iterators.
func IntersectIters2[K comparable, VL any, VR any](
	left iter.Seq2[K, VL],
	right iter.Seq2[K, VR],
) (
	onlyLeft map[K]VL,
	both map[K]ValuePair[VL, VR],
	onlyRight map[K]VR,
) {
	rightMap := maps.Collect(right)
	return intersect(left, rightMap, len(rightMap))
}

// IntersectItersKeyFunc is like [Intersect] but for value iterators,
// using provided functions to extract keys from values.
func IntersectItersKeyFunc[K comparable, VL any, VR any](
	left iter.Seq[VL],
	leftKeyFunc func(VL) K,
	right iter.Seq[VR],
	rightKeyFunc func(VR) K,
) (
	onlyLeft map[K]VL,
	both map[K]ValuePair[VL, VR],
	onlyRight map[K]VR,
) {
	rightMap := maps.Collect(uiter.MapTo2(right, withKey(rightKeyFunc)))

	return intersect(
		uiter.MapTo2(left, withKey(leftKeyFunc)),
		rightMap,
		len(rightMap),
	)
}

// IntersectItersValueFunc is like [Intersect] but for key-only iterators,
// using provided functions to compute values for each key.
func IntersectItersValueFunc[K comparable, VL any, VR any](
	left iter.Seq[K],
	leftValueFunc func(K) VL,
	right iter.Seq[K],
	rightValueFunc func(K) VR,
) (
	onlyLeft map[K]VL,
	both map[K]ValuePair[VL, VR],
	onlyRight map[K]VR,
) {
	rightMap := maps.Collect(uiter.MapTo2(right, withValue(rightValueFunc)))

	return intersect(
		uiter.MapTo2(left, withValue(leftValueFunc)),
		rightMap,
		len(rightMap),
	)
}

func intersect[K comparable, VL any, VR any](
	left iter.Seq2[K, VL],
	right map[K]VR,
	totalLenAtLeast int,
) (
	onlyLeft map[K]VL,
	both map[K]ValuePair[VL, VR],
	onlyRight map[K]VR,
) {
	// estimate items to be distributed uniformly across left/right/both
	eachSideLenEstimate := (totalLenAtLeast + 2) / 3

	onlyLeft = make(map[K]VL, eachSideLenEstimate)
	onlyRight = make(map[K]VR, eachSideLenEstimate)
	both = make(map[K]ValuePair[VL, VR], eachSideLenEstimate)

	for k, vl := range left {
		if vr, ok := right[k]; ok {
			both[k] = ValuePair[VL, VR]{Left: vl, Right: vr}
		} else {
			onlyLeft[k] = vl
		}
	}
	for k, vr := range right {
		if _, ok := both[k]; !ok {
			onlyRight[k] = vr
		}
	}
	return
}

func intersectKeys[K comparable, VR any](
	left iter.Seq[K],
	right map[K]VR,
	totalLenAtLeast int,
) (
	onlyLeft map[K]struct{},
	both map[K]struct{},
	onlyRight map[K]struct{},
) {
	// estimate items to be distributed uniformly across left/right/both
	eachSideLenEstimate := (totalLenAtLeast + 2) / 3

	onlyLeft = make(map[K]struct{}, eachSideLenEstimate)
	onlyRight = make(map[K]struct{}, eachSideLenEstimate)
	both = make(map[K]struct{}, eachSideLenEstimate)

	for k := range left {
		if _, ok := right[k]; ok {
			both[k] = struct{}{}
		} else {
			onlyLeft[k] = struct{}{}
		}
	}
	for k := range right {
		if _, ok := both[k]; !ok {
			onlyRight[k] = struct{}{}
		}
	}
	return
}

// asKey returns (k, struct{}) for use with MapTo2.
func asKey[K any](k K) (K, struct{}) {
	return k, struct{}{}
}

// withKey returns a function that extracts key from value.
func withKey[K any, V any](keyFunc func(V) K) func(V) (K, V) {
	return func(v V) (K, V) { return keyFunc(v), v }
}

// withValue returns a function that computes value from key.
func withValue[K any, V any](valueFunc func(K) V) func(K) (K, V) {
	return func(k K) (K, V) { return k, valueFunc(k) }
}
