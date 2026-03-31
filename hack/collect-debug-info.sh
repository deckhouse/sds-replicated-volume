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

# Collects comprehensive debug information for a ReplicatedVolume
# and outputs it as structured markdown suitable for LLM analysis.
#
# Usage:
#   hack/collect-debug-info.sh <RV_NAME> [--since <duration>] [--output <file>]
#
# Prerequisites: kubectl, jq

set -euo pipefail

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

NS="d8-sds-replicated-volume"
SNC_NS="d8-sds-node-configurator"
RV_LABEL_KEY="sds-replicated-volume.deckhouse.io/replicated-volume"
DRBD_NAME_PREFIX="sdsrv-"
NSENTER_BIN="/opt/deckhouse/sds/bin/nsenter.static"
NSENTER_ARGS="-t 1 -m -u -i -n -p --"

LOG_TAIL_LINES=1000

# Convert a kubectl-style duration (e.g. "1h", "30m", "2h30m") to a
# journalctl-compatible "--since" timestamp (UTC, "YYYY-MM-DD HH:MM:SS").
duration_to_journalctl_since() {
	local dur="$1"
	local total_seconds=0
	local tmp="$dur"
	while [[ -n "$tmp" ]]; do
		if [[ "$tmp" =~ ^([0-9]+)h(.*)$ ]]; then
			total_seconds=$((total_seconds + ${BASH_REMATCH[1]} * 3600))
			tmp="${BASH_REMATCH[2]}"
		elif [[ "$tmp" =~ ^([0-9]+)m(.*)$ ]]; then
			total_seconds=$((total_seconds + ${BASH_REMATCH[1]} * 60))
			tmp="${BASH_REMATCH[2]}"
		elif [[ "$tmp" =~ ^([0-9]+)s(.*)$ ]]; then
			total_seconds=$((total_seconds + ${BASH_REMATCH[1]}))
			tmp="${BASH_REMATCH[2]}"
		else
			total_seconds=3600
			break
		fi
	done
	if [[ "$total_seconds" -eq 0 ]]; then
		total_seconds=3600
	fi
	date -u -d "-${total_seconds} seconds" '+%Y-%m-%d %H:%M:%S' 2>/dev/null \
		|| date -u -v "-${total_seconds}S" '+%Y-%m-%d %H:%M:%S' 2>/dev/null \
		|| echo "1 hour ago"
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

RV_NAME=""
SINCE=""
OUTPUT=""

usage() {
	echo "Usage: $0 <RV_NAME> [--since <duration>] [--output <file>]"
	echo ""
	echo "  RV_NAME              ReplicatedVolume name (required)"
	echo "  --since <duration>   Log time window (e.g. 1h, 30m). If omitted, all available logs are collected"
	echo "  --output <file>      Write output to file instead of stdout"
	exit 1
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--since)
		SINCE="${2:?--since requires a value}"
		shift 2
		;;
	--output)
		OUTPUT="${2:?--output requires a value}"
		shift 2
		;;
	--help | -h)
		usage
		;;
	-*)
		echo "Unknown option: $1" >&2
		usage
		;;
	*)
		if [[ -z "$RV_NAME" ]]; then
			RV_NAME="$1"
		else
			echo "Unexpected argument: $1" >&2
			usage
		fi
		shift
		;;
	esac
done

if [[ -z "$RV_NAME" ]]; then
	echo "ERROR: RV_NAME is required" >&2
	usage
fi

# ---------------------------------------------------------------------------
# Prerequisites
# ---------------------------------------------------------------------------

for cmd in kubectl jq; do
	if ! command -v "$cmd" &>/dev/null; then
		echo "ERROR: $cmd is required but not found in PATH" >&2
		exit 1
	fi
done

# ---------------------------------------------------------------------------
# Output setup
# ---------------------------------------------------------------------------

if [[ -n "$OUTPUT" ]]; then
	exec >"$OUTPUT"
fi

# ---------------------------------------------------------------------------
# Helper functions
# ---------------------------------------------------------------------------

emit() {
	echo "$@"
}

section() {
	emit ""
	emit "# $1"
	emit ""
}

subsection() {
	emit ""
	emit "## $1"
	emit ""
}

subsubsection() {
	emit ""
	emit "### $1"
	emit ""
}

fence_open() {
	emit '```'"${1:-}"
}

fence_close() {
	emit '```'
}

# Runs a command, prints its output inside a fenced block.
# Usage: run_fenced <lang> <description> <cmd...>
run_fenced() {
	local lang="$1"
	local desc="$2"
	shift 2
	emit "> Command: \`$*\`"
	emit ""
	fence_open "$lang"
	"$@" 2>&1 || true
	fence_close
}

# kubectl_fenced <lang> <description> <kubectl args...>
kubectl_fenced() {
	local lang="$1"
	local desc="$2"
	shift 2
	run_fenced "$lang" "$desc" kubectl "$@"
}

# Find agent pod name on a given node. Prints pod name or empty string.
agent_pod_on_node() {
	local node="$1"
	kubectl -n "$NS" get pod -l app=agent --field-selector "spec.nodeName=$node" \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

# Find sds-node-configurator pod on a given node. Prints pod name or empty string.
snc_pod_on_node() {
	local node="$1"
	kubectl -n "$SNC_NS" get pod -l app=sds-node-configurator --field-selector "spec.nodeName=$node" \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

# Execute a command on the host via sds-node-configurator + nsenter.
# Usage: nsenter_on_node <node> <cmd...>
nsenter_on_node() {
	local node="$1"
	shift
	local pod
	pod=$(snc_pod_on_node "$node")
	if [[ -z "$pod" ]]; then
		echo "(sds-node-configurator pod not found on node $node)"
		return 0
	fi
	kubectl -n "$SNC_NS" exec "$pod" -c sds-node-configurator-agent -- \
		"$NSENTER_BIN" $NSENTER_ARGS "$@" 2>&1 || true
}

# Execute drbdsetup in the agent pod on a given node.
# Usage: drbdsetup_on_node <node> <drbdsetup args...>
drbdsetup_on_node() {
	local node="$1"
	shift
	local pod
	pod=$(agent_pod_on_node "$node")
	if [[ -z "$pod" ]]; then
		echo "(agent pod not found on node $node)"
		return 0
	fi
	kubectl -n "$NS" exec "$pod" -c agent -- drbdsetup "$@" 2>&1 || true
}

# ---------------------------------------------------------------------------
# Object resolution
# ---------------------------------------------------------------------------

emit "# Debug information for ReplicatedVolume: $RV_NAME"
emit ""
emit "Collected at: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
emit ""
if [[ -n "$SINCE" ]]; then
	emit "Log window: --since=$SINCE"
else
	emit "Log window: all available (no --since filter)"
fi
emit ""

# --- RVR names and nodes ---
RVR_JSON=$(kubectl get rvr -l "${RV_LABEL_KEY}=${RV_NAME}" -o json 2>/dev/null || echo '{"items":[]}')
RVR_NAMES=$(echo "$RVR_JSON" | jq -r '.items[].metadata.name')
RVR_NODES=()
declare -A NODE_TO_RVR
declare -A RVR_TO_NODE
declare -A RVR_TO_DRBD_NAME

if [[ -z "$RVR_NAMES" ]]; then
	echo "WARNING: No ReplicatedVolumeReplicas found for RV $RV_NAME" >&2
fi

for rvr in $RVR_NAMES; do
	node=$(echo "$RVR_JSON" | jq -r --arg name "$rvr" '.items[] | select(.metadata.name == $name) | .spec.nodeName')
	actual_name=$(kubectl get drbdr "$rvr" -o jsonpath='{.spec.actualNameOnTheNode}' 2>/dev/null || true)
	if [[ -n "$actual_name" ]]; then
		drbd_name="$actual_name"
	else
		drbd_name="${DRBD_NAME_PREFIX}${rvr}"
	fi
	RVR_NODES+=("$node")
	NODE_TO_RVR["$node"]="$rvr"
	RVR_TO_NODE["$rvr"]="$node"
	RVR_TO_DRBD_NAME["$rvr"]="$drbd_name"
done

# Deduplicate nodes
if [[ ${#RVR_NODES[@]} -gt 0 ]]; then
	UNIQUE_NODES=($(printf '%s\n' "${RVR_NODES[@]}" | sort -u))
else
	UNIQUE_NODES=()
fi

# Compute optional args for kubectl logs and journalctl.
# When --since is not set, cap journalctl output with -n to avoid
# dumping the entire kernel log since boot (can be millions of lines).
KUBECTL_SINCE_ARGS=()
JOURNALCTL_SINCE_ARGS=()
if [[ -n "$SINCE" ]]; then
	KUBECTL_SINCE_ARGS=(--since="$SINCE")
	JOURNALCTL_SINCE=$(duration_to_journalctl_since "$SINCE")
	JOURNALCTL_SINCE_ARGS=(--since "$JOURNALCTL_SINCE")
else
	JOURNALCTL_SINCE_ARGS=(-n 50000)
fi

# PV / PVC
PV_NAME="$RV_NAME"
PVC_NS=$(kubectl get pv "$PV_NAME" -o jsonpath='{.spec.claimRef.namespace}' 2>/dev/null || true)
PVC_NAME=$(kubectl get pv "$PV_NAME" -o jsonpath='{.spec.claimRef.name}' 2>/dev/null || true)

# RSC name from RVR label
RSC_NAME=""
if [[ -n "$RVR_NAMES" ]]; then
	first_rvr=$(echo "$RVR_NAMES" | head -1)
	RSC_NAME=$(echo "$RVR_JSON" | jq -r --arg name "$first_rvr" \
		'.items[] | select(.metadata.name == $name) | .metadata.labels["sds-replicated-volume.deckhouse.io/replicated-storage-class"] // ""')
fi

# Summary table
subsection "Object Summary"

emit "| Object | Names |"
emit "|--------|-------|"
emit "| ReplicatedVolume | \`$RV_NAME\` |"
if [[ -n "$RVR_NAMES" ]]; then
	emit "| ReplicatedVolumeReplicas | $(echo "$RVR_NAMES" | tr '\n' ',' | sed 's/,$//' | sed 's/,/, /g') |"
else
	emit "| ReplicatedVolumeReplicas | (none found) |"
fi
if [[ ${#UNIQUE_NODES[@]} -gt 0 ]]; then
	emit "| Nodes | $(printf '%s, ' "${UNIQUE_NODES[@]}" | sed 's/, $//') |"
else
	emit "| Nodes | (none) |"
fi
emit "| PV | \`$PV_NAME\` |"
if [[ -n "$PVC_NAME" ]]; then
	emit "| PVC | \`$PVC_NS/$PVC_NAME\` |"
fi
if [[ -n "$RSC_NAME" ]]; then
	emit "| ReplicatedStorageClass | \`$RSC_NAME\` |"
fi
emit ""

for rvr in $RVR_NAMES; do
	emit "- RVR \`$rvr\` on node \`${RVR_TO_NODE[$rvr]}\`, DRBD name: \`${RVR_TO_DRBD_NAME[$rvr]}\`"
done

# ===================================================================
# Section 1: Kubernetes Resources and Events
# ===================================================================

section "Section 1: Kubernetes Resources and Events"

# --- RV ---
subsubsection "ReplicatedVolume: $RV_NAME"
kubectl_fenced "yaml" "RV YAML" get rv "$RV_NAME" -o yaml

# --- RVRs ---
for rvr in $RVR_NAMES; do
	subsubsection "ReplicatedVolumeReplica: $rvr"
	kubectl_fenced "yaml" "RVR YAML" get rvr "$rvr" -o yaml
done

# --- RVAs ---
subsubsection "ReplicatedVolumeAttachments for $RV_NAME"

RVA_JSON=$(kubectl get rva -o json 2>/dev/null || echo '{"items":[]}')
RVA_NAMES=$(echo "$RVA_JSON" | jq -r --arg rv "$RV_NAME" '.items[] | select(.spec.replicatedVolumeName == $rv) | .metadata.name')

if [[ -z "$RVA_NAMES" ]]; then
	emit "(no RVAs found)"
else
	for rva in $RVA_NAMES; do
		emit ""
		emit "#### RVA: $rva"
		emit ""
		kubectl_fenced "yaml" "RVA YAML" get rva "$rva" -o yaml
	done
fi

# --- DRBDResources ---
for rvr in $RVR_NAMES; do
	subsubsection "DRBDResource: $rvr"
	kubectl_fenced "yaml" "DRBDResource YAML" get drbdr "$rvr" -o yaml
done

# --- PV ---
subsubsection "PersistentVolume: $PV_NAME"
kubectl_fenced "yaml" "PV YAML" get pv "$PV_NAME" -o yaml

# --- PVC ---
if [[ -n "$PVC_NAME" && -n "$PVC_NS" ]]; then
	subsubsection "PersistentVolumeClaim: $PVC_NS/$PVC_NAME"
	kubectl_fenced "yaml" "PVC YAML" -n "$PVC_NS" get pvc "$PVC_NAME" -o yaml
fi

# --- RSC ---
if [[ -n "$RSC_NAME" ]]; then
	subsubsection "ReplicatedStorageClass: $RSC_NAME"
	kubectl_fenced "yaml" "RSC YAML" get rsc "$RSC_NAME" -o yaml
fi

# --- Node conditions ---
for node in "${UNIQUE_NODES[@]}"; do
	subsubsection "Node conditions: $node"
	emit '> Command: `kubectl get node '"$node"' -o jsonpath=...conditions`'
	emit ""
	fence_open "json"
	kubectl get node "$node" -o json 2>/dev/null | jq '.status.conditions' 2>/dev/null || echo "(failed to get node conditions)"
	fence_close
done

# --- Events ---
subsubsection "Kubernetes Events"

emit "#### Events for RV $RV_NAME"
emit ""
kubectl_fenced "" "Events" get events --all-namespaces \
	--field-selector "involvedObject.name=$RV_NAME" \
	--sort-by='.lastTimestamp' 2>/dev/null

for rvr in $RVR_NAMES; do
	emit ""
	emit "#### Events for RVR $rvr"
	emit ""
	kubectl_fenced "" "Events" get events --all-namespaces \
		--field-selector "involvedObject.name=$rvr" \
		--sort-by='.lastTimestamp' 2>/dev/null
done

if [[ -n "$PVC_NAME" && -n "$PVC_NS" ]]; then
	emit ""
	emit "#### Events for PVC $PVC_NS/$PVC_NAME"
	emit ""
	kubectl_fenced "" "Events" -n "$PVC_NS" get events \
		--field-selector "involvedObject.name=$PVC_NAME" \
		--sort-by='.lastTimestamp' 2>/dev/null
fi

emit ""
emit "#### Events for PV $PV_NAME"
emit ""
kubectl_fenced "" "Events" get events --all-namespaces \
	--field-selector "involvedObject.name=$PV_NAME" \
	--sort-by='.lastTimestamp' 2>/dev/null

# ===================================================================
# Section 2: DRBD Runtime State
# ===================================================================

section "Section 2: DRBD Runtime State"

for rvr in $RVR_NAMES; do
	node="${RVR_TO_NODE[$rvr]}"
	drbd_name="${RVR_TO_DRBD_NAME[$rvr]}"

	subsubsection "DRBD status: $drbd_name on $node"

	emit "#### drbdsetup status --verbose --statistics"
	emit ""
	emit "> Node: \`$node\`, DRBD resource: \`$drbd_name\`"
	emit ""
	fence_open
	drbdsetup_on_node "$node" status "$drbd_name" --verbose --statistics
	fence_close

	emit ""
	emit "#### drbdsetup show (full config)"
	emit ""
	emit "> Node: \`$node\`, DRBD resource: \`$drbd_name\`"
	emit ""
	fence_open
	drbdsetup_on_node "$node" show "$drbd_name"
	fence_close

	emit ""
	emit "#### drbdsetup events2 --now (state snapshot)"
	emit ""
	emit "> Node: \`$node\`, DRBD resource: \`$drbd_name\`"
	emit ""
	fence_open
	drbdsetup_on_node "$node" events2 --now "$drbd_name"
	fence_close
done

# /proc/drbd per node
for node in "${UNIQUE_NODES[@]}"; do
	subsubsection "/proc/drbd on $node"
	emit "> Node: \`$node\`"
	emit ""
	fence_open
	nsenter_on_node "$node" cat /proc/drbd
	fence_close
done

# ===================================================================
# Section 3: DRBD Debugfs
# ===================================================================

section "Section 3: DRBD Debugfs"

DEBUGFS_BASE="/sys/kernel/debug/drbd/resources"

# Reads a debugfs file via nsenter. Silently skips if not found.
read_debugfs() {
	local node="$1"
	local path="$2"
	local label="$3"

	emit ""
	emit "#### $label"
	emit ""
	emit "> Node: \`$node\`, Path: \`$path\`"
	emit ""
	fence_open
	nsenter_on_node "$node" cat "$path"
	fence_close
}

for rvr in $RVR_NAMES; do
	node="${RVR_TO_NODE[$rvr]}"
	drbd_name="${RVR_TO_DRBD_NAME[$rvr]}"
	res_dir="${DEBUGFS_BASE}/${drbd_name}"

	subsubsection "Debugfs: $drbd_name on $node"

	# Resource-level
	read_debugfs "$node" "${res_dir}/in_flight_summary" "in_flight_summary"
	read_debugfs "$node" "${res_dir}/state_twopc" "state_twopc"

	# Volume-level (vnr 0)
	vol_dir="${res_dir}/volumes/0"
	read_debugfs "$node" "${vol_dir}/data_gen_id" "data_gen_id (UUIDs)"
	read_debugfs "$node" "${vol_dir}/io_frozen" "io_frozen (suspend/bitmap diagnostics)"
	read_debugfs "$node" "${vol_dir}/oldest_requests" "oldest_requests (volume)"
	read_debugfs "$node" "${vol_dir}/act_log_extents" "act_log_extents"
	read_debugfs "$node" "${vol_dir}/ed_gen_id" "ed_gen_id (exposed data UUID)"

	# Connection-level: enumerate peer directories
	conn_base="${res_dir}/connections"
	peers=$(nsenter_on_node "$node" ls "$conn_base" 2>/dev/null | tr -s ' \t\n' '\n' || true)

	if [[ -z "$peers" ]]; then
		emit ""
		emit "(no connection directories found under $conn_base)"
	else
		for peer in $peers; do
			conn_dir="${conn_base}/${peer}"

			emit ""
			emit "#### Connection: $peer"
			emit ""

			read_debugfs "$node" "${conn_dir}/debug" "connection debug ($peer)"
			read_debugfs "$node" "${conn_dir}/transport" "connection transport ($peer)"
			read_debugfs "$node" "${conn_dir}/oldest_requests" "oldest_requests (connection $peer)"

			# Peer-device-level (vnr 0)
			read_debugfs "$node" "${conn_dir}/0/proc_drbd" "peer proc_drbd ($peer, vnr 0)"
		done
	fi
done

# ===================================================================
# Section 4: Control Plane Logs
# ===================================================================

section "Section 4: Control Plane Logs"

# Build grep pattern: match RV name or any RVR/DRBD resource name
GREP_PATTERNS=("$RV_NAME")
if [[ -n "$PVC_NAME" ]]; then
	GREP_PATTERNS+=("$PVC_NAME")
fi
for rvr in $RVR_NAMES; do
	GREP_PATTERNS+=("$rvr")
	GREP_PATTERNS+=("${RVR_TO_DRBD_NAME[$rvr]}")
done
GREP_PATTERN=$(printf '%s\n' "${GREP_PATTERNS[@]}" | sort -u | paste -sd'|' -)

# --- Controller logs ---
subsubsection "Controller logs (filtered)"

CTRL_POD=$(kubectl -n "$NS" get pod -l app=controller -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)

if [[ -z "$CTRL_POD" ]]; then
	emit "(controller pod not found)"
else
	emit "> Pod: \`$CTRL_POD\`, Container: \`controller\`, Filter: \`$GREP_PATTERN\`"
	emit ""
	fence_open
	kubectl -n "$NS" logs "$CTRL_POD" -c controller "${KUBECTL_SINCE_ARGS[@]}" --tail="$LOG_TAIL_LINES" 2>/dev/null \
		| grep -E "$GREP_PATTERN" | tail -n "$LOG_TAIL_LINES" || echo "(no matching log lines)"
	fence_close
fi

# --- Agent logs per node ---
for node in "${UNIQUE_NODES[@]}"; do
	subsubsection "Agent logs on $node (filtered)"

	agent_pod=$(agent_pod_on_node "$node")
	if [[ -z "$agent_pod" ]]; then
		emit "(agent pod not found on node $node)"
		continue
	fi

	emit "> Pod: \`$agent_pod\`, Node: \`$node\`, Container: \`agent\`, Filter: \`$GREP_PATTERN\`"
	emit ""
	fence_open
	kubectl -n "$NS" logs "$agent_pod" -c agent "${KUBECTL_SINCE_ARGS[@]}" --tail="$LOG_TAIL_LINES" 2>/dev/null \
		| grep -E "$GREP_PATTERN" | tail -n "$LOG_TAIL_LINES" || echo "(no matching log lines)"
	fence_close
done

# --- Kernel logs per node ---
for node in "${UNIQUE_NODES[@]}"; do
	subsubsection "Kernel logs (dmesg/journalctl) on $node (filtered)"

	# Build per-node grep for DRBD resource names on this node
	node_drbd_names=()
	for rvr in $RVR_NAMES; do
		if [[ "${RVR_TO_NODE[$rvr]}" == "$node" ]]; then
			node_drbd_names+=("${RVR_TO_DRBD_NAME[$rvr]}")
		fi
	done
	# Also include DRBD names of peers (other replicas) that this node might mention
	for rvr in $RVR_NAMES; do
		node_drbd_names+=("${RVR_TO_DRBD_NAME[$rvr]}")
	done
	kernel_grep=$(printf '%s\n' "${node_drbd_names[@]}" | sort -u | paste -sd'|' -)

	emit "> Node: \`$node\`, Filter: \`$kernel_grep\`"
	emit ""
	fence_open
	nsenter_on_node "$node" journalctl -k --no-pager "${JOURNALCTL_SINCE_ARGS[@]}" 2>/dev/null \
		| grep -E "$kernel_grep" | tail -n "$LOG_TAIL_LINES" || echo "(no matching kernel log lines)"
	fence_close
done

# ===================================================================
# Section 5: CSI Logs
# ===================================================================

section "Section 5: CSI Logs"

# --- CSI Controller ---
subsubsection "CSI Controller logs"

CSI_CTRL_PODS=$(kubectl -n "$NS" get pod -l app=csi-controller -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)
CSI_CTRL_CONTAINERS="provisioner attacher resizer livenessprobe controller"

if [[ -z "$CSI_CTRL_PODS" ]]; then
	emit "(csi-controller pods not found)"
else
	for pod in $CSI_CTRL_PODS; do
		for container in $CSI_CTRL_CONTAINERS; do
			emit ""
			emit "#### $pod / $container (filtered)"
			emit ""
			emit "> Pod: \`$pod\`, Container: \`$container\`, Filter: \`$GREP_PATTERN\`"
			emit ""
			fence_open
			kubectl -n "$NS" logs "$pod" -c "$container" "${KUBECTL_SINCE_ARGS[@]}" --tail="$LOG_TAIL_LINES" 2>/dev/null \
				| grep -E "$GREP_PATTERN" | tail -n "$LOG_TAIL_LINES" || echo "(no matching log lines)"
			fence_close
		done
	done
fi

# --- CSI Node per replica node ---
subsubsection "CSI Node logs (per replica node)"

CSI_NODE_CONTAINERS="node-driver-registrar node"

for node in "${UNIQUE_NODES[@]}"; do
	csi_pod=$(kubectl -n "$NS" get pod -l app=csi-node --field-selector "spec.nodeName=$node" \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)

	if [[ -z "$csi_pod" ]]; then
		emit ""
		emit "(csi-node pod not found on node $node)"
		continue
	fi

	for container in $CSI_NODE_CONTAINERS; do
		emit ""
		emit "#### $csi_pod / $container on $node (filtered)"
		emit ""
		emit "> Pod: \`$csi_pod\`, Node: \`$node\`, Container: \`$container\`, Filter: \`$GREP_PATTERN\`"
		emit ""
		fence_open
		kubectl -n "$NS" logs "$csi_pod" -c "$container" "${KUBECTL_SINCE_ARGS[@]}" --tail="$LOG_TAIL_LINES" 2>/dev/null \
			| grep -E "$GREP_PATTERN" | tail -n "$LOG_TAIL_LINES" || echo "(no matching log lines)"
		fence_close
	done
done

emit ""
emit "---"
emit ""
emit "End of debug information for ReplicatedVolume: $RV_NAME"
