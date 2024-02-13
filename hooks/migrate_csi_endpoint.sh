#!/usr/bin/env bash
#
# Copyright 2023 Flant JSC
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
  export old_driver_name="linstor.csi.linbit.com"
  export new_driver_name="drbd.csi.storage.deckhouse.io"
  export old_attacher="linstor-csi-linbit-com"
  export new_attacher="drbd-csi-storage-deckhouse-io"
  export LABEL_KEY="storage.deckhouse.io/need-kubelet-restart"
  export LABEL_VALUE=""
  export SECRET_NAME="csi-migration-finished"
  export NAMESPACE="d8-sds-drbd"
  export NAMESPACE_FOR_BACKUP="d8-system"
  export timestamp="$(date +"%Y%m%d%H%M%S")"
  export affected_pvs_hash=""
  echo "Migration csi shell hook started"

  if kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" > /dev/null 2>&1; then
    echo "Secret ${NAMESPACE}/${SECRET_NAME} exists. Migration has been finished before"
    values::set sdsDrbd.internal.csiMigrationHook.completed "true"
    exit 0
  fi

  echo "Secret ${NAMESPACE}/${SECRET_NAME} does not exist. Starting csi migration"

  sc_list=$(kubectl get sc -o json | jq -r ".items[] | select(.provisioner == \"$old_driver_name\") | .metadata.name")
  pvc_pv_list=$(kubectl get pv -o json | jq -r --arg oldDriverName "$old_driver_name" '.items[] | select(.spec.csi.driver == $oldDriverName) | .spec.claimRef.namespace + "/" + .spec.claimRef.name + "/" + .metadata.name')
  pvc_list=$(kubectl get pvc --all-namespaces -o json | jq -r --arg oldDriverName "$old_driver_name" '.items[] | select(.metadata.annotations["volume.kubernetes.io/storage-provisioner"] == $oldDriverName) | .metadata.namespace + "/" + .metadata.name')
  
  if [[ -z "$sc_list" && -z "$pvc_pv_list" && -z "$pvc_list" ]]; then
    echo "No StorageClasses and PVCs/PVs to migrate. Migration not needed"
    values::set sdsDrbd.internal.csiMigrationHook.completed "true"
    kubectl -n ${NAMESPACE} create secret generic ${SECRET_NAME}
    exit 0
  fi
  
  delete_resource ${NAMESPACE} daemonset linstor-csi-node
  scale_down_pods ${NAMESPACE} linstor-csi-controller
  scale_down_pods ${NAMESPACE} linstor-affinity-controller
  scale_down_pods ${NAMESPACE} linstor-scheduler
  scale_down_pods ${NAMESPACE} sds-drbd-controller
  scale_down_pods ${NAMESPACE} linstor-scheduler-admission


  export temp_dir=$(mktemp -d)
  cd "$temp_dir"
  echo $temp_dir

  migrate_storage_classes
  migrate_pvc_pv

  nodes_with_volumes=$(kubectl get volumeattachments -o=jsonpath='{range .items[*]}{@.metadata.name}{"\t"}{@.spec.attacher}{"\t"}{@.status.attached}{"\t"}{@.spec.nodeName}{"\n"}{end}' | grep "${old_driver_name}" | grep 'true' | awk '{print $4}' | sort | uniq)
  echo nodes_with_volumes=$nodes_with_volumes

  volumeattachments_list=$(kubectl get volumeattachments.storage.k8s.io -o json | jq -r ".items[] | select(.spec.attacher == \"$old_driver_name\") | .metadata.name")
  echo volumeattachments_list=$volumeattachments_list

  for attach in $volumeattachments_list; do
    kubectl delete volumeattachments.storage.k8s.io ${attach} --wait=false
    kubectl patch volumeattachments.storage.k8s.io ${attach} --type json -p '[{"op": "remove", "path": "/metadata/finalizers"}]'
  done


  for node in $nodes_with_volumes; do
    echo "Add label ${LABEL_KEY}=${LABEL_VALUE} to node $node"
    kubectl label node $node ${LABEL_KEY}=${LABEL_VALUE} --overwrite
  done

  echo affected_pvs_hash="$affected_pvs_hash"
  if [[ -n "$affected_pvs_hash" ]]; then
    echo "Set affectedPVsHash to values"
    values::set sdsDrbd.internal.csiMigrationHook.affectedPVsHash "$affected_pvs_hash"
  fi
  values::set sdsDrbd.internal.csiMigrationHook.completed "true"
  kubectl -n ${NAMESPACE} create secret generic ${SECRET_NAME}
}

delete_resource() {
  local NAMESPACE=$1
  local RESOURCE_TYPE=$2
  local RESOURCE_NAME=$3

  if kubectl get $RESOURCE_TYPE $RESOURCE_NAME -n $NAMESPACE > /dev/null 2>&1; then
    echo "Deleting $RESOURCE_TYPE $RESOURCE_NAME in namespace $NAMESPACE"
    kubectl delete $RESOURCE_TYPE $RESOURCE_NAME -n $NAMESPACE
    wait_for_pods_scale_down "$NAMESPACE" "$APP_NAME"
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
  sc_list=$(kubectl get sc -o json | jq -r ".items[] | select(.provisioner == \"$old_driver_name\") | .metadata.name")
  echo "StorageClasses to migrate: $sc_list"
  mkdir -p "${temp_dir}/storage_classes"
  cd "${temp_dir}/storage_classes"

  for storage_class in $sc_list; do
    kubectl get sc ${storage_class} -o yaml > ${storage_class}.yaml
    cp ${storage_class}.yaml old.${storage_class}.yaml.$timestamp
    sed -i "s/${old_driver_name}/${new_driver_name}/" ${storage_class}.yaml
  done

  backup storage-classes "${temp_dir}/storage_classes"

  for storage_class in $sc_list; do
    kubectl delete sc ${storage_class}
    kubectl create -f  ${storage_class}.yaml
  done
}

migrate_pvc_pv() {
  pvc_pv_list=$(kubectl get pv -o json | jq -r --arg oldDriverName "$old_driver_name" '.items[] | select(.spec.csi.driver == $oldDriverName) | .spec.claimRef.namespace + "/" + .spec.claimRef.name + "/" + .metadata.name')
  echo "PVs/PVCs to migrate: $pvc_pv_list"

  mkdir -p "${temp_dir}/pvc_pv"
  cd "${temp_dir}/pvc_pv"

  escaped_old_driver_name=$(echo "$old_driver_name" | sed 's/\./\\./g')
  echo $escaped_old_driver_name

  if [[ -n "$pvc_pv_list" ]]; then
    affected_pvs_hash=$(echo "$pvc_pv_list" | base64 -w0 | sha256sum | awk '{print $1}')
    for pvc_pv in $pvc_pv_list; do
      old_ifs=$IFS
      IFS='/' read -r -a array <<< "$pvc_pv"
      namespace=${array[0]}
      pvc=${array[1]}
      pv=${array[2]}

      kubectl get pvc $pvc -n $namespace -o yaml > pvc-${pvc}.yaml
      kubectl get pv $pv -o yaml > pv-${pv}.yaml

      cp pvc-${pvc}.yaml old.pvc-${pvc}.yaml.$timestamp
      cp pv-${pv}.yaml old.pv-${pv}.yaml.$timestamp

      sed -i "s/$escaped_old_driver_name/$new_driver_name/g" pv-${pv}.yaml
      sed -i "s/$old_attacher/$new_attacher/g" pv-${pv}.yaml
      sed -i '/resourceVersion: /d' pv-${pv}.yaml
      sed -i '/uid: /d' pv-${pv}.yaml

      IFS=$old_ifs
    done

    backup pvc-pv "${temp_dir}/pvc_pv"

    for pvc_pv in $pvc_pv_list; do
      old_ifs=$IFS
      IFS='/' read -r -a array <<< "$pvc_pv"
      namespace=${array[0]}
      pvc=${array[1]}
      pv=${array[2]}
      kubectl delete pv $pv --wait=false # the pv will stuck in Terminating state
      kubectl patch pv $pv --type json -p '[{"op": "remove", "path": "/metadata/finalizers"}]'
      kubectl patch pvc ${pvc} -n $namespace  --type=json -p='[{"op": "replace", "path": "/metadata/annotations/volume.beta.kubernetes.io~1storage-provisioner", "value":"'$new_driver_name'"}]'
      kubectl patch pvc ${pvc} -n $namespace  --type=json -p='[{"op": "replace", "path": "/metadata/annotations/volume.kubernetes.io~1storage-provisioner", "value":"'$new_driver_name'"}]'
      kubectl create -f pv-${pv}.yaml
      kubectl patch pvc ${pvc} -n $namespace --type=json -p='[{"op": "remove", "path": "/metadata/annotations/pv.kubernetes.io~1bind-completed"}]'
      # kubectl patch pvc ${pvc} -n $namespace --type=json -p='[{"op": "remove", "path": "/metadata/annotations/pv.kubernetes.io~1bound-by-controller"}]'

      IFS=$old_ifs
    done
  else
    echo "No PVs/PVCs to migrate"
  fi

  pvc_list=$(kubectl get pvc --all-namespaces -o json | jq -r --arg oldDriverName "$old_driver_name" '.items[] | select(.metadata.annotations["volume.kubernetes.io/storage-provisioner"] == $oldDriverName) | .metadata.namespace + "/" + .metadata.name')
  echo "PVCs to migrate after PVs/PVCs migration: $pvc_list"
  if [[ -n "$pvc_list" ]]; then
    for pvc in $pvc_list; do
      old_ifs=$IFS
      IFS='/' read -r -a array <<< "$pvc"
      namespace=${array[0]}
      pvc=${array[1]}

      kubectl patch pvc ${pvc} -n $namespace  --type=json -p='[{"op": "replace", "path": "/metadata/annotations/volume.beta.kubernetes.io~1storage-provisioner", "value":"'$new_driver_name'"}]'
      kubectl patch pvc ${pvc} -n $namespace  --type=json -p='[{"op": "replace", "path": "/metadata/annotations/volume.kubernetes.io~1storage-provisioner", "value":"'$new_driver_name'"}]'

      IFS=$old_ifs
    done
  else
    echo "No PVCs to migrate after PVs/PVCs migration"
  fi
}


backup() {
  resource_name=$1
  path=$2

  echo "Backup $resource_name to secrets"
  tar -czf - -C "${path}/" . | split -b 100k - "$1.tar.gz.part."
  for part in "${resource_name}.tar.gz.part."*; do
    part_name=$(basename "$part")
    echo "Creating secret for $part_name"
    kubectl -n "${NAMESPACE_FOR_BACKUP}" create secret generic "migrate-csi-backup-${timestamp}-${part_name}" --from-file="${part}"
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
