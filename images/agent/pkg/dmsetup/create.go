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

package dmsetup

import (
	"context"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/lib/go/common/blksize"
)

// Create creates a dm-linear device that maps the entire lower device.
// The resulting device appears at /dev/mapper/<name>.
func Create(ctx context.Context, name, lowerDevicePath string) error {
	sectors, err := blksize.GetDeviceSizeInSectors(lowerDevicePath)
	if err != nil {
		return fmt.Errorf("getting device size for %q: %w", lowerDevicePath, err)
	}

	table := fmt.Sprintf("0 %d linear %s 0", sectors, lowerDevicePath)

	out, err := ExecCommandContext(ctx, dmsetupCommand, "create", name, "--table", table).CombinedOutput()
	if err != nil {
		return withOutput(fmt.Errorf("dmsetup create %q: %w", name, err), out)
	}
	return nil
}
