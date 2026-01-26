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

package drbd

import (
	"context"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// DefaultMinorStart is the starting minor number when no DRBD devices exist.
// This avoids conflicts with common device minors.
const DefaultMinorStart = 1000

// AllocateNextMinor queries drbdsetup status for all resources,
// finds the maximum minor number in use, and returns max+1.
// If no devices exist, returns DefaultMinorStart.
func AllocateNextMinor(ctx context.Context) (uint, error) {
	status, err := drbdsetup.ExecuteStatus(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("querying drbdsetup status: %w", err)
	}

	maxMinor := status.FindMaxMinor()
	if maxMinor < 0 {
		return DefaultMinorStart, nil
	}

	return uint(maxMinor + 1), nil
}
