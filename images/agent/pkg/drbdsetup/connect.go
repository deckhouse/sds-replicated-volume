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

package drbdsetup

import (
	"context"
	"errors"
	"fmt"
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

// ExecuteConnect establishes connection to a peer.
func ExecuteConnect(ctx context.Context, resource string, peerNodeID uint8) (err error) {
	args := ConnectArgs(resource, peerNodeID)
	cmd := ExecCommandContext(ctx, Command, args...)

	defer func() {
		if err != nil {
			err = fmt.Errorf("running command %s %v: %w", Command, args, err)
		}
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		switch errToExitCode(err) {
		case 123:
			err = ErrConnectDiscardNotAllowed
		case 158:
			err = ErrConnectResourceNotFound
		case 151:
			err = ErrConnectNeedStandalone
		}
		return withOutput(err, out)
	}

	return nil
}

// DisconnectArgs returns the arguments for drbdsetup disconnect command.
var DisconnectArgs = func(resource string, peerNodeID uint8) []string {
	return []string{
		"disconnect", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}
}

// ExecuteDisconnect disconnects from a peer.
func ExecuteDisconnect(ctx context.Context, resource string, peerNodeID uint8) (err error) {
	args := DisconnectArgs(resource, peerNodeID)
	cmd := ExecCommandContext(ctx, Command, args...)

	defer func() {
		if err != nil {
			err = fmt.Errorf("running command %s %v: %w", Command, args, err)
		}
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		if errToExitCode(err) == 158 {
			err = ErrDisconnectResourceNotFound
		}
		return withOutput(err, out)
	}

	return nil
}
