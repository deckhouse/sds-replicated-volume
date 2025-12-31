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

# Default values
BUILD_TAGS="ee fe"
FIX_ISSUES="false"
NEW_FROM_MERGE_BASE=""

# Function to print colored output
print_status() {
    local color=$1
    local message=$2
    echo -e "${color}${message}${NC}"
}

# Function to show usage
show_usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Run golangci-lint on the project using go tool golangci-lint.

OPTIONS:
    -h, --help              Show this help message
    -t, --tags TAGS         Build tags to use, space-separated (default: $BUILD_TAGS)
    -f, --fix               Auto-fix issues where possible
    -n, --new-from-base SHA Run linter only on files changed since merge base SHA

EXAMPLES:
    $0                                    # Run linter with default settings
    $0 --fix                             # Run linter and auto-fix issues
    $0 --tags "ee"                       # Run linter only for 'ee' build tag
    $0 --new-from-base abc123            # Run linter only on changed files

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_usage
            exit 0
            ;;
        -t|--tags)
            BUILD_TAGS="$2"
            shift 2
            ;;
        -f|--fix)
            FIX_ISSUES="true"
            shift
            ;;
        -n|--new-from-base)
            NEW_FROM_MERGE_BASE="$2"
            shift 2
            ;;
        *)
            print_status $RED "Unknown option: $1"
            show_usage
            exit 1
            ;;
    esac
done

# Run linter on a directory
run_linter() {
    local dir="$1"
    local edition="$2"
    local extra_args="$3"
    
    print_status $YELLOW "Running linter in $dir (edition: $edition)"
    
    # Change to the directory and run linter
    (cd "$dir" && {
        local linter_cmd="go tool golangci-lint run --color=always --allow-parallel-runners --build-tags $edition"
        
        if [[ "$FIX_ISSUES" == "true" ]]; then
            linter_cmd="$linter_cmd --fix"
        fi
        
        if [[ -n "$extra_args" ]]; then
            linter_cmd="$linter_cmd $extra_args"
        fi
        
        if eval "$linter_cmd"; then
            print_status $GREEN "Linter PASSED in $dir (edition: $edition)"
            return 0
        else
            print_status $RED "Linter FAILED in $dir (edition: $edition)"
            return 1
        fi
    })
}

# Main function
main() {
    print_status $GREEN "Starting golangci-lint run using go tool"
    
    # Convert space-separated tags to array
    read -ra TAGS_ARRAY <<< "$BUILD_TAGS"
    
    local basedir=$(pwd)
    local failed=false
    local extra_args=""
    
    # Prepare extra arguments
    if [[ -n "$NEW_FROM_MERGE_BASE" ]]; then
        extra_args="--new-from-merge-base=$NEW_FROM_MERGE_BASE"
        print_status $YELLOW "Running linter only on files changed since $NEW_FROM_MERGE_BASE"
    fi
    
    # Find all go.mod files in images directory
    local go_mod_files=$(find . -name "go.mod" -type f)
    
    if [[ -z "$go_mod_files" ]]; then
        print_status $RED "No go.mod files found in images directory"
        exit 1
    fi
    
    # Run linter for each go.mod file and each build tag
    for go_mod_file in $go_mod_files; do
        local dir=$(dirname "$go_mod_file")
        
        for edition in "${TAGS_ARRAY[@]}"; do
            if ! run_linter "$dir" "$edition" "$extra_args"; then
                failed=true
            fi
        done
    done
    
    # Check for uncommitted changes if --fix was used
    if [[ "$FIX_ISSUES" == "true" ]]; then
        if [[ -n "$(git status --porcelain --untracked-files=no 2>/dev/null || true)" ]]; then
            print_status $YELLOW "Linter made changes to files. Review the changes:"
            git diff --name-only
            print_status $YELLOW "To apply all changes: git add . && git commit -m 'Fix linter issues'"
        else
            print_status $GREEN "No changes made by linter"
        fi
    fi
    
    if [[ "$failed" == "true" ]]; then
        print_status $RED "Linter failed on one or more directories"
        exit 1
    else
        print_status $GREEN "All linter checks passed!"
        exit 0
    fi
}

# Run main function
main "$@"
