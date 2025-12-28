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

package rvstatusconfigdeviceminor

import (
	"errors"
	"fmt"
	"slices"
)

const MaxDeviceMinor DeviceMinor = 1_048_575 // 2^20-1

type DeviceMinor int

const DeviceMinorZero DeviceMinor = DeviceMinor(0)

type DuplicateDeviceMinorError struct {
	error
	ConflictingRVNames []string
}

func NewDeviceMinor(val int) (DeviceMinor, bool) {
	dm := DeviceMinor(val)
	if dm < DeviceMinorZero || dm > MaxDeviceMinor {
		return DeviceMinorZero, false
	}
	return dm, true
}

func (dm DeviceMinor) Increment() (DeviceMinor, bool) {
	if dm == MaxDeviceMinor {
		return MaxDeviceMinor, false
	}
	return dm + 1, true
}

func (dm DeviceMinor) Decrement() (DeviceMinor, bool) {
	if dm == DeviceMinorZero {
		return DeviceMinorZero, false
	}
	return dm - 1, true
}

type DeviceMinorCache struct {
	byRVName map[string]DeviceMinor // values are unique
	max      DeviceMinor            // maximum value in byRVName
	released []DeviceMinor          // "holes" in values in byRVName, sorted
}

func NewDeviceMinorCache() *DeviceMinorCache {
	return &DeviceMinorCache{
		byRVName: map[string]DeviceMinor{},
	}
}

func (c *DeviceMinorCache) Len() int {
	return len(c.byRVName)
}

func (c *DeviceMinorCache) ReleasedLen() int {
	return len(c.released)
}

func (c *DeviceMinorCache) Max() DeviceMinor {
	return c.max
}

func (c *DeviceMinorCache) Released() []DeviceMinor {
	return slices.Clone(c.released)
}

func (c *DeviceMinorCache) Initialize(byRVName map[string]DeviceMinor) error {
	// Validate

	// It's important to ensure DM uniqueness, because [DeviceMinorCache.Release]
	// depends on [DeviceMinorCache.max] value decrement.
	// Allowing duplicates in would lead to a corrupted state.

	// using sorted array instead of map to be able to detect holes
	dms := make([]DeviceMinor, 0, len(byRVName))
	rvNames := make([]string, 0, len(byRVName)) // same index with dms

	var dupErr DuplicateDeviceMinorError
	for rvName, dm := range byRVName {
		i, found := slices.BinarySearch(dms, dm)
		if found {
			dupErr = DuplicateDeviceMinorError{
				error:              fmt.Errorf("rvs '%s' and '%s' have same device minor %d", rvNames[i], rvName, dm),
				ConflictingRVNames: append(dupErr.ConflictingRVNames, rvNames[i], rvName),
			}
			continue
		}

		dms = slices.Insert(dms, i, dm)
		rvNames = slices.Insert(rvNames, i, rvName)
	}

	if len(dupErr.ConflictingRVNames) > 0 {
		return dupErr
	}

	// Clear state
	c.byRVName = make(map[string]DeviceMinor, len(dms))
	c.released = nil
	c.max = DeviceMinorZero

	// Update state
	for i, dm := range dms {
		c.byRVName[rvNames[i]] = dm

		// search for the hole on the left
		var holeStart DeviceMinor
		if i > 0 {
			holeStart, _ = dms[i-1].Increment()
		}
		for ; holeStart < dm; holeStart, _ = holeStart.Increment() {
			// adding a hole
			c.insertReleased(holeStart)
		}
	}
	if len(dms) > 0 {
		c.max = dms[len(dms)-1]
	}
	return nil
}

func (c *DeviceMinorCache) GetOrCreate(rvName string) (DeviceMinor, error) {
	// initialize first item
	if len(c.byRVName) == 0 {
		c.addRVDM(rvName, c.max)
		return c.max, nil
	}

	// get existing
	if dm, ok := c.byRVName[rvName]; ok {
		return dm, nil
	}

	// create - reuse released minors
	if dm, ok := c.takeFirstReleased(); ok {
		c.addRVDM(rvName, dm)
		return dm, nil
	}

	// create - new
	dm, ok := c.max.Increment()
	if !ok {
		return DeviceMinorZero, errors.New("ran out of device minors")
	}
	c.addRVDM(rvName, dm)
	return dm, nil
}

func (c *DeviceMinorCache) Release(rvName string) {
	c.removeRVDM(rvName)
}

func (c *DeviceMinorCache) addRVDM(rvName string, dm DeviceMinor) {
	c.byRVName[rvName] = dm
	c.max = max(c.max, dm)
}

func (c *DeviceMinorCache) removeRVDM(rvName string) {
	dm, ok := c.byRVName[rvName]
	if !ok {
		return
	}

	if dm == c.max {
		// decrement c.max until non-hole value is met, or collection is empty
		for {
			c.max, ok = c.max.Decrement()
			if !ok {
				// it was the last element
				break
			}
			if maxReleased, ok := c.maxReleased(); !ok || maxReleased != c.max {
				// no hole
				break
			}
			// removing a hole
			c.takeLastReleased()
		}
	} else {
		// adding a hole
		c.insertReleased(dm)
	}

	delete(c.byRVName, rvName)
}

func (c *DeviceMinorCache) takeFirstReleased() (DeviceMinor, bool) {
	if len(c.released) == 0 {
		return DeviceMinorZero, false
	}
	dm := c.released[0]
	c.released = c.released[1:]
	return dm, true
}

func (c *DeviceMinorCache) maxReleased() (DeviceMinor, bool) {
	if len(c.released) == 0 {
		return DeviceMinorZero, false
	}
	return c.released[len(c.released)-1], true
}

func (c *DeviceMinorCache) takeLastReleased() (DeviceMinor, bool) {
	if len(c.released) == 0 {
		return DeviceMinorZero, false
	}
	last := c.released[len(c.released)-1]
	c.released = c.released[:len(c.released)-1]
	return last, true
}

func (c *DeviceMinorCache) insertReleased(dm DeviceMinor) {
	// we never replace the existing value, so second return value doesn't matter
	i, _ := slices.BinarySearch(c.released, dm)
	c.released = slices.Insert(c.released, i, dm)
}
