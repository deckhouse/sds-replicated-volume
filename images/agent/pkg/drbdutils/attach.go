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

package drbdutils

import (
	"context"
	"errors"
	"strconv"
)

var (
	ErrAttachCannotOpenBackingDevice = errors.New("cannot open backing device")
	ErrAttachCannotOpenMetaDevice    = errors.New("cannot open meta device")
	ErrAttachDeviceTooSmall          = errors.New("device too small")
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

var AttachKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(104)", JoinErr: ErrAttachCannotOpenBackingDevice},
	{ExitCode: 10, OutputSubstring: "(105)", JoinErr: ErrAttachCannotOpenMetaDevice},
	{ExitCode: 10, OutputSubstring: "(116)", JoinErr: ErrAttachInvalidMetaDataIndex},
	{ExitCode: 10, OutputSubstring: "(124)", JoinErr: ErrAttachAlreadyAttached},
	{ExitCode: 10, OutputSubstring: "(165)", JoinErr: ErrAttachMetaDataUnclean},
	{ExitCode: 10, OutputSubstring: "(119)", JoinErr: ErrAttachMetaDataInvalid},
	{ExitCode: 10, OutputSubstring: "(118)", JoinErr: ErrAttachMetaDataIOError},
	{ExitCode: 10, OutputSubstring: "(111)", JoinErr: ErrAttachDeviceTooSmall},
	{ExitCode: 10, OutputSubstring: "(112)", JoinErr: ErrAttachDeviceTooSmall},
	{ExitCode: 10, OutputSubstring: "(127)", JoinErr: ErrAttachMinorNotAllocated},
}

// ExecuteAttach attaches a backing device and meta-data device to a volume.
func ExecuteAttach(ctx context.Context, minor uint, lowerDev, metaDev, metaIdx string) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, AttachArgs(minor, lowerDev, metaDev, metaIdx)...)
	_, err := executeCommand(cmd, AttachKnownErrors)
	return err
}
