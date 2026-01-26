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
	"fmt"
	"strconv"
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

// ExecuteAttach attaches a backing device and meta-data device to a volume.
func ExecuteAttach(ctx context.Context, minor uint, lowerDev, metaDev, metaIdx string) error {
	args := AttachArgs(minor, lowerDev, metaDev, metaIdx)
	cmd := ExecCommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"running command %s %v: %w; output: %q",
			Command, args, err, string(out),
		)
	}

	return nil
}
