#!/usr/bin/env bash
#
# Copyright 2024 Flant JSC
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

source /deckhouse/shell_lib.sh

get_config() {
  echo '{"configVersion":"v1", "afterHelm": 10}'
}

run_trigger() {
  export OLD_DRIVER_NAME="linstor.csi.linbit.com"
  export NEW_DRIVER_NAME="replicated.csi.storage.deckhouse.io"
  export SECRET_NAME="csi-migration-completed"
  export NAMESPACE="d8-sds-replicated-volume"
  export TIMESTAMP="$(date +"%Y%m%d%H%M%S")"
  echo "Check csi migration shell hook started"

  if kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" > /dev/null 2>&1; then
    echo "Secret ${NAMESPACE}/${SECRET_NAME} exists. Migration has been finished before. No need to check csi migration"
    values::set sdsReplicatedVolume.internal.csiMigrationHook.completed "true"
    exit 0
  fi

  echo "Secret ${NAMESPACE}/${SECRET_NAME} does not exist. Checking csi migration"

  sc_list=$(kubectl get sc -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.provisioner == $oldDriverName) | .metadata.name')
  pv_pvc_list=$(kubectl get pv -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.spec.csi.driver == $oldDriverName) | .spec.claimRef.namespace + "/" + .spec.claimRef.name + "/" + .metadata.name')
  pvc_list=$(kubectl get pvc --all-namespaces -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.metadata.annotations["volume.kubernetes.io/storage-provisioner"] == $oldDriverName) | .metadata.namespace + "/" + .metadata.name')
  volume_snapshot_classes=$(kubectl get volumesnapshotclasses.snapshot.storage.k8s.io -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.driver == $oldDriverName) | .metadata.name')
  volume_snapshot_contents=$(kubectl get volumesnapshotcontents.snapshot.storage.k8s.io -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.spec.driver == $oldDriverName) | .metadata.name')

  if [[ -z "$sc_list" && -z "$pv_pvc_list" && -z "$pvc_list" && -z "$volume_snapshot_classes" && -z "$volume_snapshot_contents" ]]; then
    echo "No StorageClasses, PVCs, PVs, VolumeSnapshotClasses, VolumeSnapshotContents to migrate. Migration not needed"
    values::set sdsReplicatedVolume.internal.csiMigrationHook.completed "true"
    kubectl -n ${NAMESPACE} create secret generic ${SECRET_NAME} -l heritage=deckhouse,module=sds-replicated-volume
    exit 0
  fi
  
  echo "StorageClasses: $sc_list"
  echo "PVs/PVCs: $pv_pvc_list"
  echo "PVCs: $pvc_list"
  echo "VolumeSnapshotClasses: $volume_snapshot_classes"
  echo "VolumeSnapshotContents: $volume_snapshot_contents"
  echo "There are resources to migrate. Fail the hook to restart the migration process"

  values::set sdsReplicatedVolume.internal.csiMigrationHook.completed "false"
  exit 1
}


if [[ ${1-} == "--config" ]] ; then
  get_config
else
  run_trigger
fi
