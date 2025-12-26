#!/usr/bin/env bash

# Print all todos in the selected folders

BASE_URL="https://github.com/deckhouse/sds-replicated-volume"
BRANCH="astef-prototype"

grep -RIn "TODO" api images/controller images/agent images/csi-driver | \
while IFS=: read -r file line text; do
  # Trim leading/trailing whitespace from the TODO line
  trimmed_text=$(printf '%s' "$text" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')

  echo "$trimmed_text"
  # Normalize path (remove leading ./ if present)
  rel="${file#./}"
  echo "${BASE_URL}/blob/${BRANCH}/${rel}#L${line}"
  echo
done