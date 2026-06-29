#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")/.."
exec go run ./e2e/pkg/debug/cmd "$@"
