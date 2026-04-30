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
	"strings"
)

var (
	ErrNewPeerResourceNotFound      = errors.New("resource not found")
	ErrNewPeerTransportCreateFailed = errors.New("failed to create transport")
	ErrDelPeerResourceNotFound      = errors.New("resource not found")
	ErrForgetPeerResourceNotFound   = errors.New("resource not found")
)

// NewPeerOptions contains optional parameters for new-peer command.
type NewPeerOptions struct {
	Name         string // Connection name (MANDATORY - used for --_name)
	Protocol     string // A, B, or C
	SharedSecret string
	CRAMHMACAlg  string // Required for shared-secret to work (e.g., "sha256", "sha1")
	RRConflict   string // "retry-connect", "disconnect", etc.
	VerifyAlg    string // Online verify hash algorithm (from /proc/crypto)
}

// NewPeerArgs returns the arguments for drbdsetup new-peer command.
var NewPeerArgs = func(resource string, peerNodeID uint8, opts *NewPeerOptions) []string {
	args := []string{
		"new-peer", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}
	if opts != nil {
		// --_name is mandatory for new-peer
		if opts.Name != "" {
			args = append(args, "--_name="+opts.Name)
		}
		if opts.Protocol != "" {
			args = append(args, "--protocol", opts.Protocol)
		}
		if opts.SharedSecret != "" {
			args = append(args, "--shared-secret", opts.SharedSecret)
		}
		if opts.CRAMHMACAlg != "" {
			// DRBD expects lowercase algorithm names (from /proc/crypto)
			args = append(args, "--cram-hmac-alg", strings.ToLower(opts.CRAMHMACAlg))
		}
		if opts.RRConflict != "" {
			args = append(args, "--rr-conflict", opts.RRConflict)
		}
		if opts.VerifyAlg != "" {
			args = append(args, "--verify-alg", opts.VerifyAlg)
		}
	}
	return args
}

var NewPeerKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrNewPeerResourceNotFound},
	{ExitCode: 10, OutputSubstring: "(172)", JoinErr: ErrNewPeerTransportCreateFailed},
}

// ExecuteNewPeer makes a peer node known to the resource.
func ExecuteNewPeer(ctx context.Context, resource string, peerNodeID uint8, opts *NewPeerOptions) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, NewPeerArgs(resource, peerNodeID, opts)...)
	_, err := executeCommand(cmd, NewPeerKnownErrors)
	return err
}

// DelPeerArgs returns the arguments for drbdsetup del-peer command.
var DelPeerArgs = func(resource string, peerNodeID uint8) []string {
	return []string{
		"del-peer", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}
}

var DelPeerKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrDelPeerResourceNotFound},
}

// ExecuteDelPeer removes a peer connection.
func ExecuteDelPeer(ctx context.Context, resource string, peerNodeID uint8) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, DelPeerArgs(resource, peerNodeID)...)
	_, err := executeCommand(cmd, DelPeerKnownErrors)
	return err
}

// ForgetPeerArgs returns the arguments for drbdsetup forget-peer command.
var ForgetPeerArgs = func(resource string, peerNodeID uint8) []string {
	return []string{
		"forget-peer", resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
	}
}

var ForgetPeerKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrForgetPeerResourceNotFound},
}

// ExecuteForgetPeer removes all references to a peer from meta-data.
func ExecuteForgetPeer(ctx context.Context, resource string, peerNodeID uint8) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, ForgetPeerArgs(resource, peerNodeID)...)
	_, err := executeCommand(cmd, ForgetPeerKnownErrors)
	return err
}
