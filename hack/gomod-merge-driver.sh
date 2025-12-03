#!/bin/bash
# Git merge driver for go.mod and go.sum
# Usage: gomod-merge-driver.sh %O %A %B %L %P
# %O = ancestor version
# %A = our version (current branch) - this is the output file
# %B = their version (branch being merged)
# %L = conflict marker size
# %P = pathname of the file being merged

set -e

O="$1"
A="$2"
B="$3"
L="$4"
P="$5"

# Get directory containing the file
FILE_DIR=$(dirname "$P")
FILE_NAME=$(basename "$P")

if [[ "$P" == *.mod ]]; then
    # For go.mod: union merge (combine all require/replace directives from both sides)
    # Use git merge-file with union strategy to combine changes
    # This merges non-conflicting changes and combines conflicting sections
    if ! git merge-file -p --union "$A" "$O" "$B" > "$A.tmp" 2>/dev/null; then
        # Fallback: if git merge-file fails, combine all unique lines
        # This preserves all dependencies from both versions
        cat "$O" "$A" "$B" 2>/dev/null | sort -u > "$A.tmp"
    fi
    mv "$A.tmp" "$A"
    
    # Run go mod tidy in the directory containing go.mod
    # This will fix formatting, resolve dependencies, and update go.sum
    cd "$FILE_DIR" || exit 1
    if command -v go >/dev/null 2>&1; then
        go mod tidy
    fi
    
    # Exit with success
    exit 0
elif [[ "$P" == *.sum ]]; then
    # For go.sum: mark as resolved by taking our version
    # go mod tidy (run during go.mod merge) will regenerate go.sum correctly
    # If go.mod is processed first, go mod tidy already updated go.sum
    # If go.sum is processed first, we take our version and go mod tidy will update it later
    # Just mark as resolved - git will use our version (file A)
    exit 0
else
    # Not a go.mod or go.sum file, shouldn't happen
    exit 1
fi

