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
	ErrNewPeerResourceNotFound      = errors.New("resource not found")
	ErrNewPeerInvalidNodeID         = errors.New("invalid peer node ID")
	ErrNewPeerTransportCreateFailed = errors.New("failed to create transport")
	ErrDelPeerResourceNotFound      = errors.New("resource not found")
	ErrForgetPeerResourceNotFound   = errors.New("resource not found")
)

// NewPeerOptions contains optional parameters for new-peer command.
type NewPeerOptions struct {
	Protocol     string // A, B, or C
	SharedSecret string
	CRAMHMACAlg  string // Required for shared-secret to work (e.g., "sha256", "sha1")
	RRConflict   string // "retry-connect", "disconnect", etc.
}

// NewPeerArgs returns the arguments for drbdsetup new-peer command.
var NewPeerArgs = func(resource string, peerNodeID uint8, opts *NewPeerOptions) []string {
	args := []string{
		"new-peer", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}
	if opts != nil {
		if opts.Protocol != "" {
			args = append(args, "--protocol", opts.Protocol)
		}
		if opts.SharedSecret != "" {
			args = append(args, "--shared-secret", opts.SharedSecret)
		}
		if opts.CRAMHMACAlg != "" {
			args = append(args, "--cram-hmac-alg", opts.CRAMHMACAlg)
		}
		if opts.RRConflict != "" {
			args = append(args, "--rr-conflict", opts.RRConflict)
		}
	}
	return args
}

// ExecuteNewPeer makes a peer node known to the resource.
func ExecuteNewPeer(ctx context.Context, resource string, peerNodeID uint8, opts *NewPeerOptions) error {
	args := NewPeerArgs(resource, peerNodeID, opts)
	cmd := ExecCommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		switch errToExitCode(err) {
		case 158:
			return ErrNewPeerResourceNotFound
		case 561:
			return ErrNewPeerInvalidNodeID
		case 562:
			return ErrNewPeerTransportCreateFailed
		}
		return fmt.Errorf(
			"running command %s %v: %w; output: %q",
			Command, args, err, string(out),
		)
	}

	return nil
}

// DelPeerArgs returns the arguments for drbdsetup del-peer command.
var DelPeerArgs = func(resource string, peerNodeID uint8) []string {
	return []string{
		"del-peer", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}
}

// ExecuteDelPeer removes a peer connection.
func ExecuteDelPeer(ctx context.Context, resource string, peerNodeID uint8) error {
	args := DelPeerArgs(resource, peerNodeID)
	cmd := ExecCommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if errToExitCode(err) == 158 {
			return ErrDelPeerResourceNotFound
		}
		return fmt.Errorf(
			"running command %s %v: %w; output: %q",
			Command, args, err, string(out),
		)
	}

	return nil
}

// ForgetPeerArgs returns the arguments for drbdsetup forget-peer command.
var ForgetPeerArgs = func(resource string, peerNodeID uint) []string {
	return []string{
		"forget-peer", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}
}

// ExecuteForgetPeer removes all references to a peer from meta-data.
func ExecuteForgetPeer(ctx context.Context, resource string, peerNodeID uint) error {
	args := ForgetPeerArgs(resource, peerNodeID)
	cmd := ExecCommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if errToExitCode(err) == 158 {
			return ErrForgetPeerResourceNotFound
		}
		return fmt.Errorf(
			"running command %s %v: %w; output: %q",
			Command, args, err, string(out),
		)
	}

	return nil
}
