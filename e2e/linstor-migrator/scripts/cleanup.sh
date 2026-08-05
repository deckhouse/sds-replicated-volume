#!/bin/bash
#
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

# Linstor Migrator E2E Cleanup Script
# This script deletes all resources created by the migrator e2e tests.
# It waits for migrator-e2e PVs to disappear after PVC deletion before removing
# ReplicatedStorageClasses and other storage resources; other steps may only
# initiate deletion without waiting.

set -e

echo "=========================================="
echo "Linstor Migrator E2E Cleanup Script"
echo "=========================================="
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}IMPORTANT: Run this script with kubectl context pointing to the TEST cluster.${NC}"
echo -e "${YELLOW}It does not clean base-cluster VirtualDisks automatically.${NC}"
echo ""

# Base delete helper with optional kubectl flags and wait mode.
delete_resources_base() {
    local resource=$1
    local description=$2
    local wait_mode=$3
    shift 3

    local wait_flag="--wait=true"
    if [ "${wait_mode}" = "false" ]; then
        wait_flag="--wait=false"
    fi

    echo -e "${YELLOW}Deleting ${description}...${NC}"
    kubectl delete ${resource} --all ${wait_flag} "$@" 2>/dev/null || {
        echo -e "${YELLOW}Note: No ${description} found or already deleted${NC}"
    }
}

# Delete namespaced resources across all namespaces
delete_resources() {
    local wait_mode=${3:-true}
    delete_resources_base "$1" "$2" "${wait_mode}" --all-namespaces
}

# Delete resources by label selector across all namespaces.
delete_resources_by_selector() {
    local resource=$1
    local description=$2
    local selector=$3
    local wait_mode=${4:-true}

    local wait_flag="--wait=true"
    if [ "${wait_mode}" = "false" ]; then
        wait_flag="--wait=false"
    fi

    echo -e "${YELLOW}Deleting ${description}...${NC}"
    kubectl delete "${resource}" --all-namespaces -l "${selector}" "${wait_flag}" 2>/dev/null || {
        echo -e "${YELLOW}Note: No ${description} found or already deleted${NC}"
    }
}

# Delete cluster-scoped resources
delete_cluster_resources() {
    local wait_mode=${3:-true}
    delete_resources_base "$1" "$2" "${wait_mode}"
}

# After PVC deletion, PVs can linger in Terminating. RSC cleanup must run only when
# those PVs are gone, otherwise LINSTOR/CSI can keep resources in use.
wait_for_migrator_pvs_removed() {
    echo -e "${YELLOW}Waiting for migrator-e2e PVs to be fully removed...${NC}"
    while true; do
        local jsonpath_matches label_matches
        jsonpath_matches="$(
            kubectl get pv -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.storageClassName}{"\t"}{.spec.claimRef.namespace}{"\t"}{.spec.claimRef.name}{"\n"}{end}' 2>/dev/null | grep 'migrator-e2e' || true
        )"
        label_matches="$(kubectl get pv -l migrator-e2e --no-headers 2>/dev/null || true)"

        if [ -z "${jsonpath_matches}" ] && [ -z "${label_matches}" ]; then
            break
        fi

        echo -e "${YELLOW}Waiting for PV cleanup (expecting no migrator-e2e PVs, poll every 2s)...${NC}"
        if [ -n "${jsonpath_matches}" ]; then
            printf '%s\n' "${jsonpath_matches}"
        fi
        if [ -n "${label_matches}" ]; then
            printf '%s\n' "${label_matches}"
        fi
        sleep 2
    done
}

# Find a running linstor-controller pod name.
find_linstor_controller_pod() {
    kubectl -n d8-sds-replicated-volume get pods -l app=linstor-controller \
        -o jsonpath='{range .items[?(@.status.phase=="Running")]}{.metadata.name}{"\n"}{end}' | head -n 1
}

# Delete lingering migrator storage pools directly from Linstor via originallinstor CLI.
cleanup_linstor_storage_pools() {
    echo -e "${YELLOW}Deleting lingering migrator storage pools in Linstor...${NC}"

    if ! kubectl -n d8-sds-replicated-volume get deployment linstor-controller >/dev/null 2>&1; then
        echo -e "${YELLOW}Note: skipping Linstor storage pool cleanup because deployment/linstor-controller does not exist${NC}"
        return 0
    fi

    local controller_pod
    controller_pod="$(find_linstor_controller_pod)"
    if [ -z "${controller_pod}" ]; then
        echo -e "${YELLOW}Note: linstor-controller pod not found; skipping direct Linstor storage pool cleanup${NC}"
        return 0
    fi

    # Storage pools can be deleted only after all Linstor resources are gone.
    # Wait until `linstor r list` returns an empty payload: [] or [[]].
    while true; do
        local resources_raw resources_compact
        resources_raw="$(kubectl -n d8-sds-replicated-volume exec "${controller_pod}" -c linstor-controller -- \
            originallinstor -m --output-version=v1 r list 2>/dev/null || true)"
        resources_compact="$(printf '%s' "${resources_raw}" | tr -d '[:space:]')"

        if [ "${resources_compact}" = "[]" ] || [ "${resources_compact}" = "[[]]" ]; then
            break
        fi

        echo -e "${YELLOW}Waiting for all Linstor resources to be removed before deleting storage pools (expecting [] or [[]], poll every 2s)...${NC}"
        echo -e "${YELLOW}Current 'linstor r list' output:${NC}"
        printf '%s\n' "${resources_raw}"
        sleep 2
    done

    local pairs
    pairs="$(kubectl -n d8-sds-replicated-volume exec "${controller_pod}" -c linstor-controller -- \
        originallinstor -m --output-version=v1 sp list | awk -F'"' '
            /"node_name"/ {node=$4}
            /"storage_pool_name"/ {
                pool=$4
                if (pool ~ /migrator-e2e/) {
                    print node "|" pool
                }
            }
        ' | sort -u)"

    if [ -z "${pairs}" ]; then
        echo -e "${YELLOW}Note: no migrator storage pools found in Linstor${NC}"
        return 0
    fi

    while IFS='|' read -r node pool; do
        [ -n "${node}" ] || continue
        [ -n "${pool}" ] || continue
        echo -e "${YELLOW}Deleting Linstor storage pool ${pool} on node ${node}...${NC}"
        kubectl -n d8-sds-replicated-volume exec "${controller_pod}" -c linstor-controller -- \
            originallinstor sp delete "${node}" "${pool}" || true
    done <<< "${pairs}"
}

# Collect thin-pool cleanup targets from LVG resources before deleting LVGs.
collect_thin_pool_targets() {
    kubectl get lvmvolumegroups.storage.deckhouse.io -l migrator-e2e -o go-template='{{- range .items -}}{{ .spec.local.nodeName }}|{{ .spec.actualVGNameOnTheNode }}|{{- range .spec.thinPools }}{{ .name }} {{- end }}{{ "\n" }}{{- end -}}'
}

# Remove thin pools directly on nodes via sds-node-configurator agent pod.
cleanup_thin_pools_on_nodes() {
    local targets=$1

    if [ -z "${targets}" ]; then
        echo -e "${YELLOW}Note: no migrator LVG thin-pool targets found${NC}"
        return 0
    fi

    echo -e "${YELLOW}Deleting thin pools on nodes (to unblock terminating LVGs)...${NC}"
    while IFS='|' read -r node vg thin_pools; do
        [ -n "${node}" ] || continue
        [ -n "${vg}" ] || continue
        [ -n "${thin_pools}" ] || continue

        local agent_pod
        agent_pod="$(kubectl -n d8-sds-node-configurator get pods --field-selector "spec.nodeName=${node}" -o name | grep '^pod/sds-node-configurator' | head -n 1 | cut -d/ -f2)"
        if [ -z "${agent_pod}" ]; then
            echo -e "${YELLOW}Note: sds-node-configurator pod not found on node ${node}; skipping thin-pool cleanup${NC}"
            continue
        fi

        for thin in ${thin_pools}; do
            [ -n "${thin}" ] || continue
            while true; do
                lv_list_raw="$(kubectl -n d8-sds-node-configurator exec "${agent_pod}" -c sds-node-configurator-agent -- \
                    /opt/deckhouse/sds/bin/nsenter -t 1 -m -u -i -n -p -- \
                    /opt/deckhouse/sds/bin/lvm.static lvs --noheadings -o lv_name "${vg}" 2>/dev/null || true)"

                echo -e "${YELLOW}Current LVs in VG ${vg} on node ${node}:${NC}"
                if [ -n "${lv_list_raw}" ]; then
                    printf '%s\n' "${lv_list_raw}"
                else
                    echo "(none)"
                fi

                non_thin_count="$(printf '%s\n' "${lv_list_raw}" | awk -v thin="${thin}" '
                    NF {
                        gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0)
                        if ($0 != "" && $0 != thin) {
                            count++
                        }
                    }
                    END {print count+0}
                ')"

                if [ "${non_thin_count}" -eq 0 ]; then
                    break
                fi

                echo -e "${YELLOW}Waiting for all LVs to be removed from VG ${vg} before deleting thin pool ${thin} (poll every 2s)...${NC}"
                sleep 2
            done

            echo -e "${YELLOW}Deleting thin pool ${vg}/${thin} on node ${node}...${NC}"
            kubectl -n d8-sds-node-configurator exec "${agent_pod}" -c sds-node-configurator-agent -- \
                /opt/deckhouse/sds/bin/nsenter -t 1 -m -u -i -n -p -- \
                /opt/deckhouse/sds/bin/lvm.static lvremove -y "${vg}/${thin}" || true
        done
    done <<< "${targets}"
}

# After thin-pool removal, LVGs can finish terminating. Wait until they are gone before
# wiping leftover device nodes, otherwise LVM may recreate /dev/migrator* entries.
wait_for_migrator_lvgs_removed() {
    echo -e "${YELLOW}Waiting for migrator-e2e LVMVolumeGroups to be fully removed...${NC}"
    while true; do
        local remaining
        remaining="$(kubectl get lvmvolumegroups.storage.deckhouse.io -l migrator-e2e --no-headers 2>/dev/null || true)"
        if [ -z "${remaining}" ]; then
            break
        fi

        echo -e "${YELLOW}Waiting for LVG cleanup (expecting no migrator-e2e LVGs, poll every 2s)...${NC}"
        printf '%s\n' "${remaining}"
        sleep 2
    done
}

# Remove leftover migrator device nodes that can stick after LVM thin-pool/LVG cleanup.
cleanup_migrator_devices_on_nodes() {
    echo -e "${YELLOW}Removing leftover /dev/migrator* and /dev/mapper/migrator* on all test-cluster nodes...${NC}"

    local node_names
    node_names="$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')"
    if [ -z "${node_names}" ]; then
        echo -e "${RED}Error: no nodes found in test cluster${NC}"
        return 1
    fi

    while IFS= read -r node; do
        [ -n "${node}" ] || continue

        local agent_pod
        agent_pod="$(kubectl -n d8-sds-node-configurator get pods \
            --field-selector "spec.nodeName=${node},status.phase=Running" -o name | \
            grep '^pod/sds-node-configurator' | head -n 1 | cut -d/ -f2)"

        if [ -z "${agent_pod}" ]; then
            echo -e "${YELLOW}Note: sds-node-configurator running pod not found on node ${node}; skipping migrator device cleanup${NC}"
            continue
        fi

        echo -e "${YELLOW}Removing /dev/migrator* and /dev/mapper/migrator* on node ${node} via pod ${agent_pod}...${NC}"
        kubectl -n d8-sds-node-configurator exec "${agent_pod}" -c sds-node-configurator-agent -- \
            /opt/deckhouse/sds/bin/nsenter -t 1 -m -u -i -n -p -- \
            sh -c 'rm -rf /dev/migrator* /dev/mapper/migrator*' || true
    done <<< "${node_names}"
}

# Remove local migrator temp directory on all test-cluster nodes.
cleanup_migrator_tmp_dir_on_nodes() {
    local target_dir="/opt/deckhouse/tmp/linstor-migrator"

    echo -e "${YELLOW}Deleting ${target_dir} on all test-cluster nodes...${NC}"

    local node_names
    node_names="$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')"
    if [ -z "${node_names}" ]; then
        echo -e "${RED}Error: no nodes found in test cluster${NC}"
        return 1
    fi

    while IFS= read -r node; do
        [ -n "${node}" ] || continue

        local agent_pod
        agent_pod="$(kubectl -n d8-sds-node-configurator get pods \
            --field-selector "spec.nodeName=${node},status.phase=Running" -o name | \
            grep '^pod/sds-node-configurator' | head -n 1 | cut -d/ -f2)"

        if [ -z "${agent_pod}" ]; then
            echo -e "${RED}Error: sds-node-configurator running pod not found on node ${node}; cannot remove ${target_dir}${NC}"
            return 1
        fi

        echo -e "${YELLOW}Removing ${target_dir} on node ${node} via pod ${agent_pod}...${NC}"
        kubectl -n d8-sds-node-configurator exec "${agent_pod}" -c sds-node-configurator-agent -- \
            /opt/deckhouse/sds/bin/nsenter -t 1 -m -u -i -n -p -- \
            rm -rf "${target_dir}"
    done <<< "${node_names}"
}

echo "Step 1: Deleting test pods and PVCs..."
delete_resources_by_selector "pod" "test pods" "migrator-e2e"
delete_resources_by_selector "pvc" "test PVCs" "migrator-e2e"
wait_for_migrator_pvs_removed

echo ""
echo "Step 2: Deleting Kubernetes resources (order matters for dependencies)..."

# Delete in reverse order of creation to handle dependencies
#delete_cluster_resources "replicatedvolumeattachments.storage.deckhouse.io" "ReplicatedVolumeAttachments"
delete_cluster_resources "replicatedvolumes.storage.deckhouse.io" "ReplicatedVolumes"
#delete_cluster_resources "replicatedvolumereplicas.storage.deckhouse.io" "ReplicatedVolumeReplicas"
#delete_cluster_resources "drbdresources.storage.deckhouse.io" "DRBDResources"
#delete_cluster_resources "lvmlogicalvolumes.storage.deckhouse.io" "LVMLogicalVolumes"

delete_cluster_resources "replicatedstorageclasses.storage.deckhouse.io" "ReplicatedStorageClasses"
delete_cluster_resources "replicatedstoragepools.storage.deckhouse.io" "ReplicatedStoragePools"
cleanup_linstor_storage_pools

thin_pool_targets="$(collect_thin_pool_targets)"
delete_cluster_resources "lvmvolumegroups.storage.deckhouse.io" "LVMVolumeGroups" "false"
cleanup_thin_pools_on_nodes "${thin_pool_targets}"
wait_for_migrator_lvgs_removed
cleanup_migrator_devices_on_nodes
delete_cluster_resources "blockdevices.storage.deckhouse.io" "BlockDevices"
cleanup_migrator_tmp_dir_on_nodes

echo ""
echo "=========================================="
echo "Detaching VirtualDisks from base cluster"
echo "=========================================="
echo ""
echo "Disks are created without labels, so cleanup in base cluster should use name pattern."
echo "Use namespace of the parent (base) cluster."
echo ""
echo "# 1) Delete VMBDA attachments created by migrator (migrator-e2e-<runid>-<idx>-attachment):"
echo "export NS=<namespace> && kubectl get virtualmachineblockdeviceattachments.virtualization.deckhouse.io \\"
echo "  -n \$NS -o name | grep 'migrator-e2e' | \\"
echo "  xargs -r -I {} kubectl delete {} -n \$NS"
echo ""
echo "# 2) Delete VirtualDisks created by migrator (migrator-e2e-<runid>-<idx>):"
echo "export NS=<namespace> && kubectl get virtualdisks.virtualization.deckhouse.io \\"
echo "  -n \$NS -o name | grep 'migrator-e2e' | \\"
echo "  xargs -r -I {} kubectl delete {} -n \$NS"
echo ""
echo ""
new_control_plane_enabled="$(kubectl get mc sds-replicated-volume -o jsonpath='{.spec.settings.newControlPlane}' 2>/dev/null || true)"
mpo_image_tag="$(kubectl get mpo sds-replicated-volume -o jsonpath='{.spec.imageTag}' 2>/dev/null || true)"
need_mpo_patch="false"
if [ -n "${mpo_image_tag}" ] && [ "${mpo_image_tag}" != "main" ]; then
    need_mpo_patch="true"
fi

if [ "${new_control_plane_enabled}" = "true" ] || [ "${need_mpo_patch}" = "true" ]; then
    echo "=========================================="
    echo "Rollback to old control plane (TEST cluster)"
    echo "=========================================="
    echo ""
    echo "# 1) Disable module:"
    echo "kubectl patch mc sds-replicated-volume --type=merge -p '{\"spec\":{\"enabled\":false}}'"
    echo ""
    echo "# 2) Roll back ModulePullOverride to main (if needed):"
    if [ "${need_mpo_patch}" = "true" ]; then
        echo "# Current mpo/sds-replicated-volume imageTag: ${mpo_image_tag}"
        echo "kubectl patch mpo sds-replicated-volume --type=merge -p '{\"spec\":{\"imageTag\":\"main\"}}'"
    else
        echo "ModulePullOverride update is not required (imageTag is already main or mpo is absent)"
    fi
    echo ""
    echo "# 3) Re-enable module with old control plane:"
    echo "kubectl patch mc sds-replicated-volume --type=json -p='[{\"op\":\"remove\",\"path\":\"/spec/settings/newControlPlane\"}]'"
    echo "kubectl patch mc sds-replicated-volume --type=merge -p '{\"spec\":{\"enabled\":true}}'"
    echo ""
fi

echo "=========================================="
echo "Verify cleanup in TEST cluster"
echo "=========================================="
echo ""
echo "kubectl get pods,pvc -A -l migrator-e2e"
echo "kubectl get rva,rv,rvr,drbdr,rsp,rsc,llv,lvg,bd"
echo "kubectl -n d8-sds-replicated-volume exec \$(kubectl -n d8-sds-replicated-volume get pods -l app=linstor-controller | grep Running | awk '{print \$1}' | head -1) -c linstor-controller -- originallinstor sp list"
echo ""
echo "# Remove finalizers from stuck resources:"
echo "kubectl get <resource> -o name | \\"
echo "  xargs -I {} kubectl patch {} -p '{\"metadata\":{\"finalizers\":[]}}' --type=merge"
echo ""
echo "=========================================="
echo "Cleanup initiated - no longer waiting"
echo "=========================================="
