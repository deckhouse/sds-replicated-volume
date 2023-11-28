#!/bin/bash

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

set -x

initial_backup="false"
scheduled_backup="false"
retention_count=3

while [[ $# -gt 0 ]]; do
    case "$1" in
        -i|--initialBackup)
            initial_backup="true"
            shift
            ;;
        -b|--scheduledBackup)
            scheduled_backup="true"
            shift
            ;;
        -c|--retentionCount)
            retention_count=$2
            shift
            ;;
        *)
            echo "Unknown key: $1"
            ;;
    esac
    shift
done

echo $initial_backup
echo $scheduled_backup
echo $retention_count

if [ $initial_backup = "false" ] && [ $scheduled_backup = "false" ]; then
  exit 0
fi

initial_backup_exists=$(kubectl get secrets -l sds-drbd.deckhouse.io/initial-backup= | wc -l)

if [ $initial_backup = "true" ] && [ $initial_backup_exists -ne 0 ]; then
  exit 0
fi

rm -r /tmp/*

kubectl get crds | grep -o ".*.internal.linstor.linbit.com" | xargs kubectl get crds -oyaml > /tmp/crds.yaml
kubectl get crds | grep -o ".*.internal.linstor.linbit.com" | xargs -i{} sh -c "kubectl get {} -oyaml > /tmp/{}.yaml"

metadata_backup=$(tar -czvf - /tmp | base64 -w0)
current_timestamp='linstor-db-backup-'$(date '+%Y-%m-%d-%H%M%S')

part_size=524288

num_parts=$(((${#metadata_backup} + $part_size - 1) / $part_size))
for ((i = 0; i < $num_parts; i++)); do
  start=$((i * part_size))
  end=$((start + part_size))

  current_part=${metadata_backup:start:end}

  echo "Part $i: $current_part"
  if [ $initial_backup_exists -eq 0 ] && [ $initial_backup = "true" ] ; then
kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: linstor-initial-backup-$i
data:
  filepart: $current_part
EOF
  fi

  if [ $scheduled_backup = "true" ] ; then
kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: $current_timestamp-$i
data:
  filepart: $current_part
EOF
  fi
done

if [ $initial_backup_exists -eq 0 ] && [ $initial_backup = "true" ]; then
  kubectl get secrets  -oname | grep linstor-initial-backup | xargs  -I {} kubectl patch {} --type=json -p '[{op: "add", path: "/metadata/labels", value: {"sds-drbd.deckhouse.io/initial-backup": ""}}]'
fi

if [ $scheduled_backup = "true" ] ; then
  kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: $current_timestamp-completed
EOF

completed_backup_list=( $(kubectl get secrets --no-headers --sort-by='{.metadata.creationTimestamp}' -oname | grep -E 'linstor-db-backup-.*-completed' | tail -n $retention_count | grep -oE 'linstor-db-backup-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}') )
for secret_name in $(kubectl get secrets --no-headers -oname | grep -oE 'linstor-db-backup-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}-[0-9]{1,}$')
do
  base_secret_name=$(echo $secret_name | grep -oE 'linstor-db-backup-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}')
  if [[ ${completed_backup_list[*]} =~ (^|[[:space:]])"$base_secret_name"($|[[:space:]]) ]]; then
      echo "Keeps $secret_name";
  else
      echo "Deleting $secret_name";
      kubectl delete secret $secret_name
  fi
done

for secret_name in $(kubectl get secrets --no-headers -oname | grep -oE 'linstor-db-backup-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}-completed')
do
  base_secret_name=$(echo $secret_name | grep -oE 'linstor-db-backup-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}')
  if [[ ${completed_backup_list[*]} =~ (^|[[:space:]])"$base_secret_name"($|[[:space:]]) ]]; then
      echo "Keeps $secret_name";
  else
      echo "Deleting $secret_name";
      kubectl delete secret $secret_name
  fi
done
fi