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

package drbdsetup

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

var (
	ErrAttachCannotOpenBackingDevice = errors.New("cannot open backing device")
	ErrAttachCannotOpenMetaDevice    = errors.New("cannot open meta device")
	ErrAttachNotBlockDevice          = errors.New("device not a block device")
	ErrAttachDeviceTooSmall          = errors.New("device too small")
	ErrAttachDeviceClaimed           = errors.New("device already claimed")
	ErrAttachInvalidMetaDataIndex    = errors.New("invalid meta-data index")
	ErrAttachMetaDataIOError         = errors.New("I/O error on meta-data")
	ErrAttachMetaDataInvalid         = errors.New("meta-data invalid or uninitialized")
	ErrAttachAlreadyAttached         = errors.New("already attached")
	ErrAttachMinorNotAllocated       = errors.New("minor not allocated")
	ErrAttachMetaDataUnclean         = errors.New("meta-data unclean")
)

// AttachArgs returns the arguments for drbdsetup attach command.
// metaDev can be "internal" for internal metadata.
// metaIdx can be "internal" or "flexible" or a numeric index.
var AttachArgs = func(minor uint, lowerDev, metaDev, metaIdx string) []string {
	return []string{
		"attach",
		strconv.FormatUint(uint64(minor), 10),
		lowerDev,
		metaDev,
		metaIdx,
	}
}

// ExecuteAttach attaches a backing device and meta-data device to a volume.
func ExecuteAttach(ctx context.Context, minor uint, lowerDev, metaDev, metaIdx string) error {
	args := AttachArgs(minor, lowerDev, metaDev, metaIdx)
	cmd := ExecCommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		switch errToExitCode(err) {
		case 104:
			return ErrAttachCannotOpenBackingDevice
		case 105:
			return ErrAttachCannotOpenMetaDevice
		case 107, 108:
			return ErrAttachNotBlockDevice
		case 111, 112:
			return ErrAttachDeviceTooSmall
		case 114, 115:
			return ErrAttachDeviceClaimed
		case 116:
			return ErrAttachInvalidMetaDataIndex
		case 118:
			return ErrAttachMetaDataIOError
		case 119:
			return ErrAttachMetaDataInvalid
		case 124:
			return ErrAttachAlreadyAttached
		case 127:
			return ErrAttachMinorNotAllocated
		case 165:
			return ErrAttachMetaDataUnclean
		}
		return fmt.Errorf(
			"running command %s %v: %w; output: %q",
			Command, args, err, string(out),
		)
	}

	return nil
}
