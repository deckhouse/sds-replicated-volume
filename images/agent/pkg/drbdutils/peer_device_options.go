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
	"strconv"
)

// PeerDeviceOptions contains options for drbdsetup peer-device-options command.
type PeerDeviceOptions struct {
	Bitmap       *bool
	CPlanAhead   *string
	CDelayTarget *string
	CFillTarget  *string
	CMaxRate     *string
	CMinRate     *string
}

// PeerDeviceOptionsArgs returns arguments for drbdsetup peer-device-options command.
var PeerDeviceOptionsArgs = func(resource string, peerNodeID uint8, volumeNr uint, opts PeerDeviceOptions) []string {
	args := []string{
		"peer-device-options", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
		strconv.FormatUint(uint64(volumeNr), 10),
	}

	if opts.Bitmap != nil {
		if *opts.Bitmap {
			args = append(args, "--bitmap=yes")
		} else {
			args = append(args, "--bitmap=no")
		}
	}
	if opts.CPlanAhead != nil {
		args = append(args, "--c-plan-ahead="+*opts.CPlanAhead)
	}
	if opts.CDelayTarget != nil {
		args = append(args, "--c-delay-target="+*opts.CDelayTarget)
	}
	if opts.CFillTarget != nil {
		args = append(args, "--c-fill-target="+*opts.CFillTarget)
	}
	if opts.CMaxRate != nil {
		args = append(args, "--c-max-rate="+*opts.CMaxRate)
	}
	if opts.CMinRate != nil {
		args = append(args, "--c-min-rate="+*opts.CMinRate)
	}

	return args
}

// ExecutePeerDeviceOptions changes peer-device options on an existing connection volume.
func ExecutePeerDeviceOptions(ctx context.Context, resource string, peerNodeID uint8, volumeNr uint, opts PeerDeviceOptions) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, PeerDeviceOptionsArgs(resource, peerNodeID, volumeNr, opts)...)
	_, err := executeCommand(cmd, nil)
	return err
}
