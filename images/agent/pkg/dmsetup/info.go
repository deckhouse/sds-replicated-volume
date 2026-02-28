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
	"strconv"
	"strings"
)

// DeviceInfo holds parsed output from dmsetup info.
type DeviceInfo struct {
	Name      string
	State     string
	OpenCount int
}

// Info retrieves information about a device-mapper device.
// Returns nil DeviceInfo and nil error if the device does not exist.
func Info(ctx context.Context, name string) (*DeviceInfo, error) {
	out, err := ExecCommandContext(ctx, dmsetupCommand, "info", name).CombinedOutput()
	if err != nil {
		outStr := string(out)
		if strings.Contains(outStr, "No such device or address") ||
			strings.Contains(outStr, "Device does not exist") {
			return nil, nil
		}
		return nil, withOutput(fmt.Errorf("dmsetup info %q: %w", name, err), out)
	}

	return parseInfo(string(out))
}

func parseInfo(output string) (*DeviceInfo, error) {
	info := &DeviceInfo{}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Name":
			info.Name = value
		case "State":
			info.State = value
		case "Open count":
			count, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("parsing open count %q: %w", value, err)
			}
			info.OpenCount = count
		}
	}

	return info, nil
}
