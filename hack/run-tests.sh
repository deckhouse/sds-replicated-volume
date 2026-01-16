#!/bin/bash

# Copyright 2025 Flant JSC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

print_status() {
    local color=$1
    local message=$2
    echo -e "${color}${message}${NC}"
}

print_status "$YELLOW" "Starting test run..."

# Get all workspace modules from go.work
modules=$(go work edit -json | jq -r '.Use[].DiskPath')

if [ -z "$modules" ]; then
    print_status "$RED" "No modules found in go.work"
    exit 1
fi

# Track results
total_modules=0
failed_modules=0
passed_modules=0

for mod in $modules; do
    print_status "$YELLOW" "Testing $mod"
    total_modules=$((total_modules + 1))

    if (cd "$mod" && go test -v ./...); then
        print_status "$GREEN" "✓ PASSED: $mod"
        passed_modules=$((passed_modules + 1))
    else
        print_status "$RED" "✗ FAILED: $mod"
        failed_modules=$((failed_modules + 1))
    fi
    echo
done

# Print summary
echo "=========================================="
print_status "$YELLOW" "Test Summary:"
echo "Total modules: $total_modules"
print_status "$GREEN" "Passed: $passed_modules"
if [ $failed_modules -gt 0 ]; then
    print_status "$RED" "Failed: $failed_modules"
    exit 1
else
    print_status "$GREEN" "Failed: $failed_modules"
    print_status "$GREEN" "All tests passed!"
    exit 0
fi
