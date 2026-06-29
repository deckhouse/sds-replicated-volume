#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")/.."

PRESET="${1:?Usage: $0 <smoke|fast|safe|all|self-tests>}"
SUITE="${E2E_SUITE:-control-plane}"
SUITE_DIR="./e2e/${SUITE}/"
EXTRA_ARGS=""

case "$PRESET" in
  smoke)       LABEL_FILTER="Smoke" ;;
  fast)        LABEL_FILTER="Smoke || Full" ;;
  safe)        LABEL_FILTER="!Disruptive" ;;
  all)         LABEL_FILTER="" ;;
  self-tests)  SUITE_DIR="./e2e/pkg/framework/selftest/"
               LABEL_FILTER=""
               EXTRA_ARGS="-- --self-tests" ;;
  *)           echo "Unknown preset: $PRESET"; exit 1 ;;
esac

NODES=$(kubectl get nodes \
  -l storage.deckhouse.io/sds-replicated-volume-node \
  -o name 2>/dev/null | wc -l | tr -d ' ')
WORKERS=$(( NODES < 5 ? NODES : 5 ))
if [ "$WORKERS" -lt 1 ]; then
  WORKERS=1
fi

GINKGO_ARGS=(-r -p --procs="$WORKERS")
[ -n "$LABEL_FILTER" ] && GINKGO_ARGS+=(--label-filter="$LABEL_FILTER")
[ -n "${E2E_FLAKE_ATTEMPTS:-}" ] && \
  GINKGO_ARGS+=(--flake-attempts="$E2E_FLAKE_ATTEMPTS")

exec ginkgo "${GINKGO_ARGS[@]}" "$SUITE_DIR" $EXTRA_ARGS
