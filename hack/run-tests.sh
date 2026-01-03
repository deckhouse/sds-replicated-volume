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

# Function to print colored output
print_status() {
    local color=$1
    local message=$2
    echo -e "${color}${message}${NC}"
}

print_status $YELLOW "Starting test run..."

# Find all directories with test files
test_dirs=$(find . -name "*_test.go" -exec dirname {} \; | sort -u)

if [ -z "$test_dirs" ]; then
    print_status $YELLOW "No test files found"
    exit 0
fi

# Track overall results
total_packages=0
failed_packages=0
passed_packages=0

# Run tests for each directory
for dir in $test_dirs; do
    if [ ! -d "$dir" ]; then
        continue
    fi

    print_status $YELLOW "Testing $dir"
    total_packages=$((total_packages + 1))

    # Some test directories live in nested Go modules that are NOT part of the root go.work.
    # For such modules, we must disable workspace mode (GOWORK=off) so `go test` uses the nearest go.mod.
    #
    # For modules that ARE in go.work, we must keep workspace mode enabled, otherwise those modules may fail
    # due to incomplete go.sum (they rely on go.work wiring).
    #
    # Keep this list in sync with go.work "use (...)".
    test_cmd=(go test -v)
    case "$dir" in
        ./api/*|./images/controller/*|./internal/*|./lib/go/common/*)
            test_cmd=(go test -v)
            ;;
        *)
            test_cmd=(env GOWORK=off go test -v)
            ;;
    esac

    if (cd "$dir" && "${test_cmd[@]}"); then
        print_status $GREEN "✓ PASSED: $dir"
        passed_packages=$((passed_packages + 1))
    else
        print_status $RED "✗ FAILED: $dir"
        failed_packages=$((failed_packages + 1))
    fi
    echo
done

# Print summary
echo "=========================================="
print_status $YELLOW "Test Summary:"
echo "Total packages: $total_packages"
print_status $GREEN "Passed: $passed_packages"
if [ $failed_packages -gt 0 ]; then
    print_status $RED "Failed: $failed_packages"
    exit 1
else
    print_status $GREEN "Failed: $failed_packages"
    print_status $GREEN "All tests passed!"
    exit 0
fi
