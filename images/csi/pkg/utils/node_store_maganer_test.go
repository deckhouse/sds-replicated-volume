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
	"testing"

	"github.com/stretchr/testify/assert"
	mountutils "k8s.io/mount-utils"

	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/pkg/logger"
)

func TestNodeStoreManager(t *testing.T) {
	t.Run("toMapperPath", func(t *testing.T) {
		t.Run("does_not_have_prefix_returns_empty", func(t *testing.T) {
			assert.Equal(t, "", toMapperPath("not-dev-path"))
		})

		t.Run("have_prefix_returns_path", func(t *testing.T) {
			path := "/dev/some-good/path"
			expected := "/dev/mapper/some--good-path"

			assert.Equal(t, expected, toMapperPath(path))
		})
	})

	t.Run("checkMount", func(t *testing.T) {
		t.Run("all_good", func(t *testing.T) {
			const (
				devPath = "/dev/some-good/path"
				target  = "some-target"
			)
			f := &mountutils.FakeMounter{}
			f.MountPoints = []mountutils.MountPoint{
				{
					Device: devPath,
					Path:   target,
				},
			}
			store := &Store{
				Log: &logger.Logger{},
				NodeStorage: mountutils.SafeFormatAndMount{
					Interface: f,
				},
			}

			err := checkMount(store, devPath, target, []string{})
			assert.NoError(t, err)
		})

		t.Run("device_is_not_devPath_nor_mapperDevPath_returns_error", func(t *testing.T) {
			const (
				devPath = "weird-path"
				target  = "some-target"
			)
			f := &mountutils.FakeMounter{}
			f.MountPoints = []mountutils.MountPoint{
				{
					Device: "other-name",
					Path:   target,
				},
			}
			store := &Store{
				Log: &logger.Logger{},
				NodeStorage: mountutils.SafeFormatAndMount{
					Interface: f,
				},
			}

			err := checkMount(store, devPath, target, []string{})
			assert.ErrorContains(t, err, "[checkMount] device from mount point \"other-name\" does not match expected source device path weird-path or mapper device path ")
		})

		t.Run("path_is_not_target_returns_error", func(t *testing.T) {
			const (
				devPath = "weird-path"
				target  = "some-target"
			)
			f := &mountutils.FakeMounter{}
			f.MountPoints = []mountutils.MountPoint{
				{
					Device: devPath,
					Path:   "other-path",
				},
			}
			store := &Store{
				Log: &logger.Logger{},
				NodeStorage: mountutils.SafeFormatAndMount{
					Interface: f,
				},
			}

			err := checkMount(store, devPath, target, []string{})
			assert.ErrorContains(t, err, "[checkMount] mount point \"some-target\" not found in mount info")
		})
	})
}
