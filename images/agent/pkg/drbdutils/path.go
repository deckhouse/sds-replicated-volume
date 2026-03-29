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
	ErrNewPathLocalAddrInUse  = errors.New("local address already in use")
	ErrNewPathRemoteAddrInUse = errors.New("remote address already in use")
	ErrNewPathAddrPairInUse   = errors.New("address pair combination already in use")
	ErrDelPathNotFound        = errors.New("resource or path not found")
)

// NewPathArgs returns the arguments for drbdsetup new-path command.
// localAddr and remoteAddr should be in format "ip:port" (e.g., "192.168.1.1:7788").
var NewPathArgs = func(resource string, peerNodeID uint8, localAddr, remoteAddr string) []string {
	return []string{
		"new-path", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
		localAddr,
		remoteAddr,
	}
}

var NewPathKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(102)", JoinErr: ErrNewPathLocalAddrInUse},
	{ExitCode: 10, OutputSubstring: "(103)", JoinErr: ErrNewPathRemoteAddrInUse},
	{ExitCode: 10, OutputSubstring: "(173)", JoinErr: ErrNewPathAddrPairInUse},
}

// ExecuteNewPath adds a network path (address pair) to a peer.
func ExecuteNewPath(ctx context.Context, resource string, peerNodeID uint8, localAddr, remoteAddr string) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, NewPathArgs(resource, peerNodeID, localAddr, remoteAddr)...)
	_, err := executeCommand(cmd, NewPathKnownErrors)
	return err
}

// DelPathArgs returns the arguments for drbdsetup del-path command.
var DelPathArgs = func(resource string, peerNodeID uint8, localAddr, remoteAddr string) []string {
	return []string{
		"del-path", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
		localAddr,
		remoteAddr,
	}
}

var DelPathKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrDelPathNotFound},
	{ExitCode: 10, OutputSubstring: "no such path", JoinErr: ErrDelPathNotFound},
}

// ExecuteDelPath removes a network path from a peer.
func ExecuteDelPath(ctx context.Context, resource string, peerNodeID uint8, localAddr, remoteAddr string) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, DelPathArgs(resource, peerNodeID, localAddr, remoteAddr)...)
	_, err := executeCommand(cmd, DelPathKnownErrors)
	return err
}
