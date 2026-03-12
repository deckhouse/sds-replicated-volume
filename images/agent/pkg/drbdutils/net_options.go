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
	ErrNetOptionsProtocolVersion     = errors.New("protocol version 100+ required for online changes")
	ErrNetOptionsClearTwoPrimaries   = errors.New("cannot clear allow-two-primaries with both primaries")
	ErrNetOptionsVerifyAlgDuringSync = errors.New("cannot change verify-alg during verify")
	ErrNetOptionsCSumsAlgDuringSync  = errors.New("cannot change csums-alg during resync")
)

// NetOptions contains options for drbdsetup net-options command.
type NetOptions struct {
	Protocol          *string
	SharedSecret      *string
	CRAMHMACAlg       *string
	AllowTwoPrimaries *bool
	AllowRemoteRead   *bool
}

// NetOptionsArgs returns arguments for drbdsetup net-options command.
var NetOptionsArgs = func(resource string, peerNodeID uint8, opts NetOptions) []string {
	args := []string{
		"net-options", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}

	if opts.Protocol != nil {
		args = append(args, "--protocol="+*opts.Protocol)
	}

	if opts.SharedSecret != nil {
		args = append(args, "--shared-secret="+*opts.SharedSecret)
	}

	if opts.CRAMHMACAlg != nil {
		args = append(args, "--cram-hmac-alg="+*opts.CRAMHMACAlg)
	}

	if opts.AllowTwoPrimaries != nil {
		if *opts.AllowTwoPrimaries {
			args = append(args, "--allow-two-primaries=yes")
		} else {
			args = append(args, "--allow-two-primaries=no")
		}
	}

	if opts.AllowRemoteRead != nil {
		if *opts.AllowRemoteRead {
			args = append(args, "--allow-remote-read=yes")
		} else {
			args = append(args, "--allow-remote-read=no")
		}
	}

	return args
}

var NetOptionsKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(163)", JoinErr: ErrNetOptionsProtocolVersion},
	{ExitCode: 10, OutputSubstring: "(164)", JoinErr: ErrNetOptionsClearTwoPrimaries},
	{ExitCode: 10, OutputSubstring: "(149)", JoinErr: ErrNetOptionsVerifyAlgDuringSync},
	{ExitCode: 10, OutputSubstring: "(148)", JoinErr: ErrNetOptionsCSumsAlgDuringSync},
}

// ExecuteNetOptions changes network options on an existing connection.
func ExecuteNetOptions(ctx context.Context, resource string, peerNodeID uint8, opts NetOptions) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, NetOptionsArgs(resource, peerNodeID, opts)...)
	_, err := executeCommand(cmd, NetOptionsKnownErrors)
	return err
}
