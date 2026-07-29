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
	ErrLockResourceNotFound = errors.New("resource not found")
	ErrLockHeld             = errors.New("admin_lock is held by another node")
	ErrNotLockHolder        = errors.New("unlock rejected: holder/generation mismatch")
	ErrLockBusy             = errors.New("admin_lock acquire timed out waiting for local resync drain")
	ErrLockNotHeld          = errors.New("admin_lock is not held")
	ErrLockNotSupported     = errors.New("a peer does not advertise DRBD_FF_ADMIN_LOCK")
)

var LockArgs = func(resource string) []string {
	return []string{"lock", resource}
}

var UnlockArgs = func(resource string, expectedHolderNodeID *int8, expectedGeneration *uint32) []string {
	args := []string{"unlock", resource}
	if expectedHolderNodeID != nil {
		args = append(args, "--expected-holder-node-id="+strconv.FormatInt(int64(*expectedHolderNodeID), 10))
	}
	if expectedGeneration != nil {
		args = append(args, "--expected-generation="+strconv.FormatUint(uint64(*expectedGeneration), 10))
	}
	return args
}

var ForceUnlockArgs = func(resource string) []string {
	return []string{"force-unlock", resource}
}

var LockKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrLockResourceNotFound},
	{ExitCode: 10, OutputSubstring: "(176)", JoinErr: ErrLockHeld},
	{ExitCode: 10, OutputSubstring: "(178)", JoinErr: ErrLockBusy},
	{ExitCode: 10, OutputSubstring: "(180)", JoinErr: ErrLockNotSupported},
}

var UnlockKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrLockResourceNotFound},
	{ExitCode: 10, OutputSubstring: "(177)", JoinErr: ErrNotLockHolder},
	{ExitCode: 10, OutputSubstring: "(179)", JoinErr: ErrLockNotHeld},
	{ExitCode: 10, OutputSubstring: "(180)", JoinErr: ErrLockNotSupported},
}

var ForceUnlockKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrLockResourceNotFound},
}

// ExecuteLock acquires the cluster-wide admin lock on a resource. The acquire
// is idempotent: if this node is already the holder, the call returns nil.
//
// On the kernel side this serializes through adm_mutex, then waits up to
// admin_lock_wait_timeout (a per-resource option, default 120s) for any local
// resync to finish, then runs a TWOPC_ADMIN_LOCK across all connected peers.
// Wrap calls in a context.WithTimeout sized for the longest expected drain.
func ExecuteLock(ctx context.Context, resource string) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, LockArgs(resource)...)
	_, err := executeCommand(cmd, LockKnownErrors)
	return err
}

// ExecuteUnlock releases the cluster-wide admin lock on a resource.
//
// expectedHolderNodeID and expectedGeneration are optional: when set, the
// kernel verifies them against the live holder/generation and rejects with
// ErrNotLockHolder if they do not match. Pass them when reconciling from a
// known baseline (e.g. RVS-controller releasing its own previously-acquired
// lock); leave nil for unconditional release by a trusted caller.
//
// Releasing a lock that is not held returns nil (idempotent) when no
// expectations are supplied; otherwise it returns ErrLockNotHeld.
func ExecuteUnlock(ctx context.Context, resource string, expectedHolderNodeID *int8, expectedGeneration *uint32) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, UnlockArgs(resource, expectedHolderNodeID, expectedGeneration)...)
	_, err := executeCommand(cmd, UnlockKnownErrors)
	return err
}

// ExecuteForceUnlock unilaterally clears the local admin_lock and best-effort
// broadcasts the new state to connected peers. It bypasses adm_mutex on the
// local node and does NOT use TWOPC.
//
// USE ONLY when the previous holder is known to be permanently gone and the
// normal unlock path is unavailable (e.g. holder node died, unlock rejected
// across the cluster). Always returns nil locally on success even if some
// peers were unreachable; those peers will reconcile via the regular handshake
// on next reconnect.
func ExecuteForceUnlock(ctx context.Context, resource string) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, ForceUnlockArgs(resource)...)
	_, err := executeCommand(cmd, ForceUnlockKnownErrors)
	return err
}
