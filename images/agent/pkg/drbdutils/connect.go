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
	ErrConnectDiscardNotAllowed   = errors.New("discard-my-data not allowed when primary")
	ErrConnectResourceNotFound    = errors.New("resource not found")
	ErrConnectNeedStandalone      = errors.New("need to be standalone")
	ErrDisconnectResourceNotFound = errors.New("resource not found")
)

// ConnectArgs returns the arguments for drbdsetup connect command.
var ConnectArgs = func(resource string, peerNodeID uint8) []string {
	return []string{
		"connect", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}
}

var ConnectKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(123)", JoinErr: ErrConnectDiscardNotAllowed},
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrConnectResourceNotFound},
	{ExitCode: 10, OutputSubstring: "(125)", JoinErr: ErrConnectNeedStandalone},
}

// ExecuteConnect establishes connection to a peer.
func ExecuteConnect(ctx context.Context, resource string, peerNodeID uint8) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, ConnectArgs(resource, peerNodeID)...)
	_, err := executeCommand(cmd, ConnectKnownErrors)
	return err
}

// DisconnectArgs returns the arguments for drbdsetup disconnect command.
var DisconnectArgs = func(resource string, peerNodeID uint8) []string {
	return []string{
		"disconnect", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}
}

var DisconnectKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrDisconnectResourceNotFound},
}

// ExecuteDisconnect disconnects from a peer.
func ExecuteDisconnect(ctx context.Context, resource string, peerNodeID uint8) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, DisconnectArgs(resource, peerNodeID)...)
	_, err := executeCommand(cmd, DisconnectKnownErrors)
	return err
}
