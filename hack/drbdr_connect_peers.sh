#!/bin/bash

# Copyright 2026 Flant JSC
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

# Source this file and call drbdr_connect_peers with a list of DRBDResource names.
#
# Usage:
#   source hack/drbdr_connect_peers.sh
#   drbdr_connect_peers drbdr-1 drbdr-2 drbdr-3
#
# Prerequisites: kubectl, jq

# --- Helper functions ---

# drbdr_get <name>
# Fetches the DRBDResource JSON by name.
# Prints the JSON to stdout. Returns non-zero on failure.
drbdr_get() {
	local name="$1"
	if [[ -z "$name" ]]; then
		echo "ERROR: drbdr_get: name is required" >&2
		return 1
	fi
	kubectl get drbdr "$name" -o json
}

# drbdr_is_configured <json>
# Checks that the DRBDResource has Configured condition with status=True.
# Returns 0 if configured, 1 otherwise.
drbdr_is_configured() {
	local json="$1"
	local status
	status=$(echo "$json" | jq -r '
		[.status.conditions // [] | .[] | select(.type == "Configured")] | .[0].status // "Unknown"
	')
	[[ "$status" == "True" ]]
}

# drbdr_has_addresses <json>
# Checks that the DRBDResource has non-empty status.addresses.
# Returns 0 if addresses are present, 1 otherwise.
drbdr_has_addresses() {
	local json="$1"
	local count
	count=$(echo "$json" | jq '[.status.addresses // [] | .[]] | length')
	[[ "$count" -gt 0 ]]
}

# drbdr_configured_observed_generation <json>
# Prints the observedGeneration from the Configured condition, or 0 if absent.
drbdr_configured_observed_generation() {
	local json="$1"
	echo "$json" | jq '
		[.status.conditions // [] | .[] | select(.type == "Configured")] | .[0].observedGeneration // 0
	'
}

# drbdr_generation <json>
# Prints metadata.generation from the DRBDResource JSON.
drbdr_generation() {
	local json="$1"
	echo "$json" | jq '.metadata.generation'
}

# drbdr_wait_configured <name> [timeout_seconds]
# Waits for the DRBDResource to have Configured=True with
# observedGeneration == metadata.generation.
# Default timeout: 120 seconds.
drbdr_wait_configured() {
	local name="$1"
	local timeout="${2:-120}"
	local deadline=$((SECONDS + timeout))

	echo "  Waiting for $name to become Configured (timeout: ${timeout}s)..."
	while [[ $SECONDS -lt $deadline ]]; do
		local json
		json=$(drbdr_get "$name" 2>/dev/null) || { sleep 2; continue; }

		local generation
		generation=$(drbdr_generation "$json")

		local observed_gen
		observed_gen=$(drbdr_configured_observed_generation "$json")

		if drbdr_is_configured "$json" && [[ "$observed_gen" == "$generation" ]]; then
			echo "  $name: Configured (observedGeneration=$observed_gen == generation=$generation)"
			return 0
		fi

		sleep 2
	done

	echo "  ERROR: Timed out waiting for $name to become Configured" >&2
	return 1
}

# _drbdr_build_peers_json <self_name> <names...>
# Builds the peers JSON array for <self_name> from the other DRBDRs.
# Expects associative array _drbdr_jsons to be set by the caller.
# Prints the JSON array to stdout.
_drbdr_build_peers_json() {
	local self_name="$1"
	shift
	local names=("$@")

	local peers="[]"
	for peer_name in "${names[@]}"; do
		if [[ "$peer_name" == "$self_name" ]]; then
			continue
		fi

		local peer_json="${_drbdr_jsons[$peer_name]}"

		# Build paths from status.addresses
		local paths
		paths=$(echo "$peer_json" | jq '[.status.addresses[] | {
			systemNetworkName: .systemNetworkName,
			address: .address
		}]')

		# Build peer object using spec fields + status.addresses
		local peer
		peer=$(echo "$peer_json" | jq --argjson paths "$paths" '{
			name: .spec.nodeName,
			type: (.spec.type // "Diskful"),
			allowRemoteRead: true,
			nodeID: .spec.nodeID,
			protocol: "C",
			paths: $paths
		}')

		peers=$(echo "$peers" | jq --argjson peer "$peer" '. + [$peer]')
	done

	echo "$peers"
}

# --- Main function ---

# drbdr_connect_peers <name1> <name2> [name3...]
# Links all listed DRBDResources by patching spec.peers using information
# from status.addresses of the other resources.
#
# Steps:
#   1. Fetch and validate each DRBDR (must be Configured, must have addresses).
#   2. For each DRBDR, compute peers from all others and patch spec.peers.
#   3. Wait for all DRBDRs to become Configured with observedGeneration == generation.
drbdr_connect_peers() {
	if [[ $# -lt 2 ]]; then
		echo "Usage: drbdr_connect_peers <name1> <name2> [name3...]" >&2
		return 1
	fi

	local names=("$@")

	# Associative array holding fetched JSON per DRBDR name.
	declare -A _drbdr_jsons

	# --- Phase 1: Fetch and validate ---
	echo "=== Phase 1: Fetching and validating DRBDResources ==="
	for name in "${names[@]}"; do
		echo "  Fetching $name..."
		local json
		json=$(drbdr_get "$name") || {
			echo "  ERROR: Failed to fetch DRBDResource $name" >&2
			return 1
		}

		if ! drbdr_is_configured "$json"; then
			echo "  ERROR: $name is not Configured (condition Configured != True)" >&2
			return 1
		fi

		if ! drbdr_has_addresses "$json"; then
			echo "  ERROR: $name has no status.addresses" >&2
			return 1
		fi

		_drbdr_jsons["$name"]="$json"
		echo "  $name: OK"
	done

	# --- Phase 2: Build and apply peers ---
	echo ""
	echo "=== Phase 2: Patching spec.peers ==="
	for name in "${names[@]}"; do
		local peers
		peers=$(_drbdr_build_peers_json "$name" "${names[@]}")

		local peer_count
		peer_count=$(echo "$peers" | jq 'length')

		local patch
		patch=$(jq -n --argjson peers "$peers" '{"spec": {"peers": $peers}}')

		echo "  Patching $name with $peer_count peer(s)..."
		kubectl patch drbdr "$name" --type=merge -p "$patch" || {
			echo "  ERROR: Failed to patch $name" >&2
			return 1
		}
	done

	# --- Phase 3: Wait for Configured with current generation ---
	echo ""
	echo "=== Phase 3: Waiting for all DRBDResources to become Configured ==="
	local failed=0
	for name in "${names[@]}"; do
		if ! drbdr_wait_configured "$name"; then
			failed=1
		fi
	done

	if [[ $failed -ne 0 ]]; then
		echo ""
		echo "ERROR: Some DRBDResources did not become Configured in time" >&2
		return 1
	fi

	echo ""
	echo "=== All DRBDResources are Configured ==="
	return 0
}
