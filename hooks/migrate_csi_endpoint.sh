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
  echo '{"configVersion":"v1", "beforeHelm": 10}'
}

run_trigger() {
  export OLD_DRIVER_NAME="linstor.csi.linbit.com"
  export NEW_DRIVER_NAME="replicated.csi.storage.deckhouse.io"
  export OLD_ATTACHER="linstor-csi-linbit-com"
  export NEW_ATTACHER="replicated-csi-storage-deckhouse-io"
  export LABEL_KEY="storage.deckhouse.io/need-kubelet-restart"
  export LABEL_VALUE=""
  export SECRET_NAME="csi-migration-completed"
  export NAMESPACE="d8-sds-replicated-volume"
  export TIMESTAMP="$(date +"%Y%m%d%H%M%S")"
  export WEBHOOK_NAME="d8-sds-replicated-volume-sc-validation"
  echo "Migration csi shell hook started"

  if kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" > /dev/null 2>&1; then
    echo "Secret ${NAMESPACE}/${SECRET_NAME} exists. Migration has been finished before"
    values::set sdsReplicatedVolume.internal.csiMigrationHook.completed "true"
    exit 0
  fi

  echo "Secret ${NAMESPACE}/${SECRET_NAME} does not exist. Starting csi migration"

  sc_list=$(kubectl get sc -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.provisioner == $oldDriverName) | .metadata.name')
  pv_pvc_list=$(kubectl get pv -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.spec.csi.driver == $oldDriverName) | .spec.claimRef.namespace + "/" + .spec.claimRef.name + "/" + .metadata.name')
  pvc_list_before_migrate=$(kubectl get pvc --all-namespaces -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.metadata.annotations["volume.kubernetes.io/storage-provisioner"] == $oldDriverName) | .metadata.namespace + "/" + .metadata.name')
  volume_snapshot_classes=$(kubectl get volumesnapshotclasses.snapshot.storage.k8s.io -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.driver == $oldDriverName) | .metadata.name')
  volume_snapshot_contents=$(kubectl get volumesnapshotcontents.snapshot.storage.k8s.io -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.spec.driver == $oldDriverName) | .metadata.name')

  if [[ -z "$sc_list" && -z "$pv_pvc_list" && -z "$pvc_list_before_migrate" && -z "$volume_snapshot_classes" && -z "$volume_snapshot_contents" ]]; then
    echo "No StorageClasses, PVCs, PVs, VolumeSnapshotClasses, VolumeSnapshotContents to migrate. Migration not needed"
    values::set sdsReplicatedVolume.internal.csiMigrationHook.completed "true"
    kubectl -n ${NAMESPACE} create secret generic ${SECRET_NAME}
    exit 0
  fi
  
  delete_resource ${NAMESPACE} daemonset linstor-csi-node
  scale_down_pods ${NAMESPACE} linstor-csi-controller
  scale_down_pods ${NAMESPACE} linstor-affinity-controller
  scale_down_pods ${NAMESPACE} linstor-scheduler
  scale_down_pods ${NAMESPACE} sds-replicated-volume-controller
  scale_down_pods ${NAMESPACE} linstor-scheduler-admission


  export temp_dir=$(mktemp -d)
  cd "$temp_dir"
  echo "temporary dir: $temp_dir"

  export sc_list=sc_list
  migrate_storage_classes

  export AFFECTED_PVS_HASH=""
  export pv_pvc_list=pv_pvc_list
  migrate_pvc_pv

  export volume_snapshot_classes=volume_snapshot_classes
  migrate_volume_snapshot_classes

  export volume_snapshot_contents=volume_snapshot_contents
  migrate_volume_snapshot_contents
  
  nodes_with_volumes=$(kubectl get volumeattachments -o=json | jq -r --arg oldDriverName "${OLD_DRIVER_NAME}" '.items[] | select(.spec.attacher == $oldDriverName) | .spec.nodeName` | sort | uniq)
  
  echo nodes_with_volumes=$nodes_with_volumes

  delete_old_volume_attachments


  for node in $nodes_with_volumes; do
    echo "Add label ${LABEL_KEY}=${LABEL_VALUE} to node $node"
    kubectl label node $node ${LABEL_KEY}=${LABEL_VALUE} --overwrite
  done

  echo AFFECTED_PVS_HASH="$AFFECTED_PVS_HASH"
  if [[ -n "$AFFECTED_PVS_HASH" ]]; then
    echo "Set affectedPVsHash to values"
    values::set sdsReplicatedVolume.internal.csiMigrationHook.affectedPVsHash "$AFFECTED_PVS_HASH"
  fi
  values::set sdsReplicatedVolume.internal.csiMigrationHook.completed "true"
  kubectl -n ${NAMESPACE} create secret generic ${SECRET_NAME}
}

delete_resource() {
  local NAMESPACE=$1
  local RESOURCE_TYPE=$2
  local RESOURCE_NAME=$3

  if kubectl get $RESOURCE_TYPE $RESOURCE_NAME -n $NAMESPACE > /dev/null 2>&1; then
    echo "Deleting $RESOURCE_TYPE $RESOURCE_NAME in namespace $NAMESPACE"
    kubectl delete $RESOURCE_TYPE $RESOURCE_NAME -n $NAMESPACE
    wait_for_pods_scale_down "$NAMESPACE" "$RESOURCE_NAME"
  else
    echo "$RESOURCE_TYPE $RESOURCE_NAME does not exist in namespace $NAMESPACE"
  fi
}

scale_down_pods() {
  local NAMESPACE=$1
  local APP_NAME=$2

  if [[ $(kubectl get pods -n "$NAMESPACE" -l app="$APP_NAME" --no-headers 2>/dev/null | wc -l) -eq 0 ]]; then
    echo "No pods with label app=$APP_NAME in namespace $NAMESPACE"
    return
  fi

  echo "Scaling down pods with label app=$APP_NAME in namespace $NAMESPACE"
  kubectl scale deployment -n "$NAMESPACE" --replicas=0 "$APP_NAME"
  wait_for_pods_scale_down "$NAMESPACE" "$APP_NAME"
}

migrate_storage_classes() {
  sc_list=$(kubectl get sc -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.provisioner == $oldDriverName) | .metadata.name')
  echo "StorageClasses to migrate: $sc_list"
  mkdir -p "${temp_dir}/storage_classes"
  cd "${temp_dir}/storage_classes"

  for storage_class in $sc_list; do
    kubectl get sc ${storage_class} -o yaml > ${storage_class}.yaml
    cp ${storage_class}.yaml old.${storage_class}.yaml.$TIMESTAMP
    sed -i "s/${OLD_DRIVER_NAME}/${NEW_DRIVER_NAME}/" ${storage_class}.yaml
  done

  backup storage-classes "${temp_dir}/storage_classes" "${temp_dir}"

  echo "Starting migration of StorageClasses..."

  if kubectl get validatingwebhookconfigurations.admissionregistration.k8s.io "${WEBHOOK_NAME}" &>/dev/null; then
    echo "Deleting validatingwebhookconfiguration ${WEBHOOK_NAME}"
    kubectl delete validatingwebhookconfigurations.admissionregistration.k8s.io "${WEBHOOK_NAME}"
  else
    echo "ValidatingWebhookConfiguration ${WEBHOOK_NAME} does not exist. Skipping deletion"
  fi

  for storage_class in $sc_list; do
    kubectl delete sc ${storage_class}
    kubectl create -f  ${storage_class}.yaml
  done
}

migrate_pvc_pv() {
  echo "PVs/PVCs to migrate: $pv_pvc_list"

  mkdir -p "${temp_dir}/pvc_pv"
  cd "${temp_dir}/pvc_pv"

  escaped_OLD_DRIVER_NAME=$(echo "$OLD_DRIVER_NAME" | sed 's/\./\\./g')
  echo $escaped_OLD_DRIVER_NAME

  if [[ -n "$pv_pvc_list" ]]; then
    AFFECTED_PVS_HASH=$(echo "$pv_pvc_list" | base64 -w0 | sha256sum | awk '{print $1}')
    for pvc_pv in $pv_pvc_list; do
      old_ifs=$IFS
      IFS='/' read -r -a array <<< "$pvc_pv"
      namespace=${array[0]}
      pvc=${array[1]}
      pv=${array[2]}

      kubectl get pvc $pvc -n $namespace -o yaml > pvc-${pvc}.yaml
      kubectl get pv $pv -o yaml > pv-${pv}.yaml

      cp pvc-${pvc}.yaml old.pvc-${pvc}.yaml.$TIMESTAMP
      cp pv-${pv}.yaml old.pv-${pv}.yaml.$TIMESTAMP

      sed -i "s/$escaped_OLD_DRIVER_NAME/$NEW_DRIVER_NAME/g" pv-${pv}.yaml
      sed -i "s/$OLD_ATTACHER/$NEW_ATTACHER/g" pv-${pv}.yaml
      sed -i '/resourceVersion: /d' pv-${pv}.yaml
      sed -i '/uid: /d' pv-${pv}.yaml

      IFS=$old_ifs
    done

    backup pvc-pv "${temp_dir}/pvc_pv" "${temp_dir}"

    echo "Starting migration of PVs/PVCs..."

    for pvc_pv in $pv_pvc_list; do
      old_ifs=$IFS
      IFS='/' read -r -a array <<< "$pvc_pv"
      namespace=${array[0]}
      pvc=${array[1]}
      pv=${array[2]}
      echo "Deleting pv: $pv"
      kubectl delete pv $pv --wait=false # the pv will stuck in Terminating state
      echo "Deleting finalizer from pv: $pv"
      kubectl patch pv $pv --type json -p '[{"op": "remove", "path": "/metadata/finalizers"}]'
      echo "Patching annotations in pvc: $pvc"
      kubectl patch pvc ${pvc} -n $namespace  --type=json -p='[{"op": "replace", "path": "/metadata/annotations/volume.beta.kubernetes.io~1storage-provisioner", "value":"'$NEW_DRIVER_NAME'"}]'
      kubectl patch pvc ${pvc} -n $namespace  --type=json -p='[{"op": "replace", "path": "/metadata/annotations/volume.kubernetes.io~1storage-provisioner", "value":"'$NEW_DRIVER_NAME'"}]'
      echo "Recreating pv: $pv"
      kubectl create -f pv-${pv}.yaml
      echo "Deleting annotation from pvc $pvc to trigger pvc/pv binding"
      kubectl patch pvc ${pvc} -n $namespace --type=json -p='[{"op": "remove", "path": "/metadata/annotations/pv.kubernetes.io~1bind-completed"}]'

      IFS=$old_ifs
    done
  else
    echo "No PVs/PVCs to migrate"
  fi

  pvc_list_after_migrate=$(kubectl get pvc --all-namespaces -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.metadata.annotations["volume.kubernetes.io/storage-provisioner"] == $oldDriverName) | .metadata.namespace + "/" + .metadata.name')
  echo "PVCs to migrate after PVs/PVCs migration: $pvc_list_after_migrate"
  if [[ -n "$pvc_list_after_migrate" ]]; then
    for pvc in $pvc_list_after_migrate; do
      old_ifs=$IFS
      IFS='/' read -r -a array <<< "$pvc"
      namespace=${array[0]}
      pvc=${array[1]}

      kubectl patch pvc ${pvc} -n $namespace  --type=json -p='[{"op": "replace", "path": "/metadata/annotations/volume.beta.kubernetes.io~1storage-provisioner", "value":"'$NEW_DRIVER_NAME'"}]'
      kubectl patch pvc ${pvc} -n $namespace  --type=json -p='[{"op": "replace", "path": "/metadata/annotations/volume.kubernetes.io~1storage-provisioner", "value":"'$NEW_DRIVER_NAME'"}]'

      IFS=$old_ifs
    done
  else
    echo "No PVCs to migrate after PVs/PVCs migration"
  fi
}

migrate_volume_snapshot_classes() {
  echo "VolumeSnapshotClasses to migrate: $volume_snapshot_classes"

  for volume_snapshot_class in $volume_snapshot_classes; do
    kubectl patch volumesnapshotclasses.snapshot.storage.k8s.io ${volume_snapshot_class} --type json -p '[{"op": "replace", "path": "/driver", "value": "'$NEW_DRIVER_NAME'"}]'
  done
}

migrate_volume_snapshot_contents() {
  echo "VolumeSnapshotContents to migrate: $volume_snapshot_contents"

  for volume_snapshot_content in $volume_snapshot_contents; do
    kubectl patch volumesnapshotcontents.snapshot.storage.k8s.io ${volume_snapshot_content} --type json -p '[{"op": "replace", "path": "/spec/driver", "value": "'$NEW_DRIVER_NAME'"}]'
  done
} 

delete_old_volume_attachments(){
  volumeattachments_list=$(kubectl get volumeattachments.storage.k8s.io -o json | jq -r --arg oldDriverName "$OLD_DRIVER_NAME" '.items[] | select(.spec.attacher == $oldDriverName) | .metadata.name')
  echo volumeattachments_list=$volumeattachments_list

  for attach in $volumeattachments_list; do
    echo "Deleting volumeattachment: $attach"
    kubectl delete volumeattachments.storage.k8s.io ${attach} --wait=false
    echo "Deleting finalizer from volumeattachment: $attach"
    kubectl patch volumeattachments.storage.k8s.io ${attach} --type json -p '[{"op": "remove", "path": "/metadata/finalizers"}]'
  done
}

backup() {
  resource_name=$1
  path=$2
  archive_dir=$3

  echo "Creating archive of $resource_name resources from ${path}, storing in ${archive_dir}, and splitting it into parts of 100kB each."
  tar -czf - -C "${path}/" . | split -b 100k - "${archive_dir}/${resource_name}.tar.gz.part."

  for part in "${archive_dir}/${resource_name}.tar.gz.part."*; do
    part_name=$(basename "$part")
    base64_data=$(base64 -w 0 < "$part")
    echo "Creating ReplicatedStorageMetadataBackup resource from part $part_name, file ${part}."
    kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStorageMetadataBackup
metadata:
  name: migrate-csi-backup-${TIMESTAMP}-${part_name}
spec:
  data: |
    ${base64_data}
EOF
  done

  echo "Finished creating ReplicatedStorageMetadataBackup resources for ${resource_name} resources."
  echo "Add labels to ReplicatedStorageMetadataBackup resources"
  for part in "${archive_dir}/${resource_name}.tar.gz.part."*; do
    part_name=$(basename "$part")
    kubectl label replicatedstoragemetadatabackup migrate-csi-backup-${TIMESTAMP}-${part_name} sds-replicated-volume.deckhouse.io/sds-replicated-volume-csi-backup-for-${resource_name}=${TIMESTAMP} --overwrite
  done
}

wait_for_pods_scale_down() {
  local NAMESPACE=$1
  local APP_NAME=$2

  local count=0
  local max_attempts=60

  until [[ $(kubectl get pods -n "${NAMESPACE}" -l app="${APP_NAME}" --no-headers 2>/dev/null | wc -l) -eq 0 ]] || [[ ${count} -eq ${max_attempts} ]]; do
    echo "Waiting for pods to be deleted in namespace=${NAMESPACE} with label app="${APP_NAME}"... Attempt ${count}/${max_attempts}."
    sleep 5
    count=$((count + 1))
  done
  
  if [[ $count -eq $max_attempts ]]; then
    echo "Timeout reached. Pods were not deleted."
    exit 1
  fi
  echo "Pods were deleted in namespace=$NAMESPACE with label app="$APP_NAME""

}

if [[ ${1-} == "--config" ]] ; then
  get_config
else
  run_trigger
fi
