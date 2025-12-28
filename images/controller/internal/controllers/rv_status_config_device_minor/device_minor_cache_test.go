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

package rvstatusconfigdeviceminor_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	. "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_device_minor"
)

type testDeviceMinorCache struct {
	*testing.T
	*DeviceMinorCache
}

func TestDeviceMinorCache(t *testing.T) {
	testDeviceMinorCache{t, NewDeviceMinorCache()}.
		// []
		expectEmpty().
		// [a, b, c, d, e, f, g, h]
		getOrCreate("a", 0, "").
		getOrCreate("b", 1, "").
		getOrCreate("c", 2, "").
		getOrCreate("d", 3, "").
		getOrCreate("e", 4, "").
		getOrCreate("f", 5, "").
		getOrCreate("g", 6, "").
		getOrCreate("h", 7, "").
		expect(8, 7, nil).
		// -
		getOrCreate("a", 0, "").
		getOrCreate("b", 1, "").
		getOrCreate("b", 1, "").
		getOrCreate("h", 7, "").
		getOrCreate("a", 0, "").
		getOrCreate("a", 0, "").
		expect(8, 7, nil).
		// [_, b, c, d, e, f, g, h]
		release("a").
		expect(7, 7, holes(0)).
		// -
		release("x").
		expect(7, 7, holes(0)).
		// -
		release("y").
		expect(7, 7, holes(0)).
		// [_, _, c, d, e, f, g, h]
		release("b").
		expect(6, 7, holes(0, 1)).
		// [_, _, c, d, e, _, g, h]
		release("f").
		expect(5, 7, holes(0, 1, 5)).
		// [_, _, c, d, e, _, _, h]
		release("g").
		expect(4, 7, holes(0, 1, 5, 6)).
		// [_, _, c, d, e]
		release("h").
		expect(3, 4, holes(0, 1)).
		// [a, _, c, d, e]
		getOrCreate("a", 0, "").
		expect(4, 4, holes(1)).
		// [a, _, _, d, e]
		release("c").
		expect(3, 4, holes(1, 2)).
		// [a, _, _, _, e]
		release("d").
		expect(2, 4, holes(1, 2, 3)).
		// [_, _, _, _, e]
		release("a").
		expect(1, 4, holes(0, 1, 2, 3)).
		// []
		release("e").
		expect(0, 0, nil).
		// [a, _, _, _, e]
		initialize(map[string]DeviceMinor{"a": 0, "e": 4}, "").
		expect(2, 4, holes(1, 2, 3)).
		// -
		initialize(map[string]DeviceMinor{"a": 0, "e": 4}, "").
		expect(2, 4, holes(1, 2, 3)).
		// - (error message order depends on map iteration, so check for key parts)
		initializeErrContains(map[string]DeviceMinor{"a": 99, "e": 99}, "a", "e", "have same device minor 99").
		expect(2, 4, holes(1, 2, 3)).
		// [a, b, _, _, e]
		getOrCreate("b", 1, "").
		expect(3, 4, holes(2, 3)).
		// [a, b, c, _, e]
		getOrCreate("c", 2, "").
		expect(4, 4, holes(3)).
		// [a, b, c, d, e]
		getOrCreate("d", 3, "").
		expect(5, 4, nil).
		// [a, b, c, d, e, f, g, h]
		getOrCreate("f", 5, "").
		getOrCreate("g", 6, "").
		getOrCreate("h", 7, "").
		expect(8, 7, nil).
		// [A, B, C, _, _, F, G, H]
		initialize(map[string]DeviceMinor{
			"A": 0,
			"B": 1,
			"C": 2,
			"F": 5,
			"G": 6,
			"H": 7,
		}, "").
		expect(6, 7, holes(3, 4)).
		// -
		getOrCreate("F", 5, "").
		getOrCreate("H", 7, "").
		getOrCreate("G", 6, "").
		getOrCreate("F", 5, "").
		getOrCreate("C", 2, "").
		getOrCreate("B", 1, "").
		getOrCreate("A", 0, "").
		expect(6, 7, holes(3, 4)).
		// [_, _, _, _, _, F]
		initialize(map[string]DeviceMinor{"F": 5}, "").
		expect(1, 5, holes(0, 1, 2, 3, 4)).
		// -
		getOrCreate("F", 5, "").
		expect(1, 5, holes(0, 1, 2, 3, 4)).
		// [_, _, ..., M]
		initialize(map[string]DeviceMinor{"M": MaxDeviceMinor}, "").
		expectLen(1).
		expectMax(MaxDeviceMinor).
		// [1, 2, ..., M]
		getOrCreateMany(int(MaxDeviceMinor), "").
		expectLen(int(MaxDeviceMinor)+1).
		expectMax(MaxDeviceMinor).
		// -
		getOrCreate("E", DeviceMinorZero, "ran out of device minors").
		expectLen(int(MaxDeviceMinor) + 1).
		expectMax(MaxDeviceMinor).
		// []
		cleanup()
}

func (tc testDeviceMinorCache) getOrCreate(rvName string, expectedDM DeviceMinor, expectedErr string) testDeviceMinorCache {
	tc.Helper()
	dm, err := tc.GetOrCreate(rvName)
	if dm != expectedDM {
		tc.Fatalf("expected GetOrCreate result to be %d, got %d", expectedDM, dm)
	}
	if !errIsExpected(err, expectedErr) {
		tc.Fatalf("expected GetOrCreate error to be %s, got %v", expectedErr, err)
	}
	return tc
}

func (tc testDeviceMinorCache) getOrCreateMany(num int, expectedErr string) testDeviceMinorCache {
	tc.Helper()
	for i := range num {
		_, err := tc.GetOrCreate(strconv.Itoa(i))
		if !errIsExpected(err, expectedErr) {
			tc.Fatalf("expected GetOrCreate error to be %s, got %v", expectedErr, err)
		}
	}
	return tc
}

func (tc testDeviceMinorCache) release(rvName string) testDeviceMinorCache {
	tc.Helper()
	tc.Release(rvName)
	return tc
}

func (tc testDeviceMinorCache) initialize(
	byRVName map[string]DeviceMinor,
	expectedErr string,
) testDeviceMinorCache {
	tc.Helper()
	err := tc.Initialize(byRVName)
	if !errIsExpected(err, expectedErr) {
		tc.Fatalf("expected Initialize error to be %s, got %v", expectedErr, err)
	}
	return tc
}

func (tc testDeviceMinorCache) initializeErrContains(
	byRVName map[string]DeviceMinor,
	substrings ...string,
) testDeviceMinorCache {
	tc.Helper()
	err := tc.Initialize(byRVName)
	if !errContainsAll(err, substrings...) {
		tc.Fatalf("expected Initialize error to contain %v, got %v", substrings, err)
	}
	return tc
}

func (tc testDeviceMinorCache) expect(
	expectedLen int,
	expectedMax DeviceMinor,
	expectedReleased []DeviceMinor,
) testDeviceMinorCache {
	tc.Helper()
	return tc.expectLen(expectedLen).expectMax(expectedMax).expectReleased(expectedReleased...)
}

func (tc testDeviceMinorCache) expectLen(expectedLen int) testDeviceMinorCache {
	tc.Helper()
	actualLen := tc.Len()
	if expectedLen != actualLen {
		tc.Fatalf("expected Len() to return %d, got %d", expectedLen, actualLen)
	}
	return tc
}

func (tc testDeviceMinorCache) expectMax(expectedMax DeviceMinor) testDeviceMinorCache {
	tc.Helper()
	actualMax := tc.Max()
	if expectedMax != actualMax {
		tc.Fatalf("expected Max() to return %d, got %d", expectedMax, actualMax)
	}
	return tc
}

func (tc testDeviceMinorCache) expectReleased(expectedReleased ...DeviceMinor) testDeviceMinorCache {
	tc.Helper()
	actualReleased := tc.Released()
	if !slices.Equal(expectedReleased, actualReleased) {
		tc.Fatalf("expected Released() to return %v, got %v", expectedReleased, actualReleased)
	}
	return tc
}

func (tc testDeviceMinorCache) cleanup() testDeviceMinorCache {
	tc.Helper()
	return tc.initialize(nil, "").expectEmpty()
}

func (tc testDeviceMinorCache) expectEmpty() testDeviceMinorCache {
	tc.Helper()
	return tc.expectLen(0).expectMax(0).expectReleased()
}

func errIsExpected(err error, expectedErr string) bool {
	return ((err == nil) == (expectedErr == "")) && (err == nil || err.Error() == expectedErr)
}

func errContainsAll(err error, substrings ...string) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	for _, s := range substrings {
		if !strings.Contains(errStr, s) {
			return false
		}
	}
	return true
}

// only for test cases to look better
func holes(d ...DeviceMinor) []DeviceMinor {
	return d
}
