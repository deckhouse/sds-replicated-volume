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
	ErrResourceOptionsResourceNotFound = errors.New("resource not found")
)

// ResourceOptions contains options for drbdsetup resource-options command.
type ResourceOptions struct {
	AutoPromote                *bool
	OnNoQuorum                 string // "suspend-io" | "io-error"
	OnNoDataAccessible         string // "suspend-io" | "io-error"
	OnSuspendedPrimaryOutdated string // "disconnect" | "force-secondary"
	Quorum                     *uint  // nil = don't set, 0 = off
	QuorumMinimumRedundancy    *uint  // nil = don't set, 0 = off
	QuorumDynamicVoters        *bool  // nil = don't set (flant extension)
}

// ResourceOptionsArgs returns arguments for drbdsetup resource-options command.
var ResourceOptionsArgs = func(resource string, opts ResourceOptions) []string {
	args := []string{"resource-options", resource}

	if opts.AutoPromote != nil {
		if *opts.AutoPromote {
			args = append(args, "--auto-promote=yes")
		} else {
			args = append(args, "--auto-promote=no")
		}
	}

	if opts.OnNoQuorum != "" {
		args = append(args, "--on-no-quorum", opts.OnNoQuorum)
	}

	if opts.OnNoDataAccessible != "" {
		args = append(args, "--on-no-data-accessible", opts.OnNoDataAccessible)
	}

	if opts.OnSuspendedPrimaryOutdated != "" {
		args = append(args, "--on-suspended-primary-outdated", opts.OnSuspendedPrimaryOutdated)
	}

	if opts.Quorum != nil {
		if *opts.Quorum == 0 {
			args = append(args, "--quorum", "off")
		} else {
			args = append(args, "--quorum", strconv.FormatUint(uint64(*opts.Quorum), 10))
		}
	}

	if opts.QuorumMinimumRedundancy != nil {
		if *opts.QuorumMinimumRedundancy == 0 {
			args = append(args, "--quorum-minimum-redundancy", "off")
		} else {
			args = append(args, "--quorum-minimum-redundancy", strconv.FormatUint(uint64(*opts.QuorumMinimumRedundancy), 10))
		}
	}

	if opts.QuorumDynamicVoters != nil {
		if *opts.QuorumDynamicVoters {
			args = append(args, "--quorum-dynamic-voters=yes")
		} else {
			args = append(args, "--quorum-dynamic-voters=no")
		}
	}

	return args
}

// ExecuteResourceOptions changes options of an existing resource.
func ExecuteResourceOptions(ctx context.Context, resource string, opts ResourceOptions) (err error) {
	args := ResourceOptionsArgs(resource, opts)
	cmd := ExecCommandContext(ctx, Command, args...)

	defer func() {
		if err != nil {
			err = fmt.Errorf("running command %s %v: %w", Command, args, err)
		}
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		if errToExitCode(err) == 158 {
			err = ErrResourceOptionsResourceNotFound
		}
		return withOutput(err, out)
	}

	return nil
}
