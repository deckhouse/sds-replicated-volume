#!/usr/bin/env python3
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

from datetime import datetime

import argparse
import base64
import kubernetes
import os
import re
import tarfile
import tempfile
import yaml

objGroup = 'storage.deckhouse.io'
objKind = 'ReplicatedStorageMetadataBackup'
objKindPlural = 'replicatedstoragemetadatabackups'
objVersion = 'v1alpha1'
linstorCRDGroup = 'internal.linstor.linbit.com'
srvBackupLabelKey = 'sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup'
backup_type = os.getenv('BACKUP_TYPE')

def create_backup(backup_date, labels={}):
    temp_path = tempfile.mkdtemp()
    backup_archive = tarfile.open(f'{temp_path}/sds-replicated-volume-{backup_type}-{backup_date}.tar', "w")

    for item in kubernetes.client.ApiextensionsV1Api().list_custom_resource_definition().items:
        if item.spec.group != linstorCRDGroup:
            continue

        crd_name_plural = item.spec.names.plural
        version = item.spec.versions[-1]
        crd_version = version.name

        custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group=linstorCRDGroup,
                                                                                        plural=crd_name_plural,
                                                                                        version=crd_version)

        with open(f'{temp_path}/{crd_name_plural}.yaml', 'w') as file:
            yaml.dump(custom_object, file)
        backup_archive.add(f'{temp_path}/{crd_name_plural}.yaml')

    backup_archive.close()

    with open(f'{temp_path}/sds-replicated-volume-{backup_type}-{backup_date}.tar', 'rb') as backup_archive:
        encoded_string = base64.b64encode(backup_archive.read())

    step = 262144 # 256KB
    for chunk_pos in range(0, len(encoded_string) // step + 1, 1):
        objName = f'sds-replicated-volume-{backup_type}-{backup_date}-backup-{chunk_pos}'
        isCreated = kubernetes.client.CustomObjectsApi().create_cluster_custom_object(group=objGroup,
                                           version=objVersion,
                                           plural=objKindPlural,
                                           body={
                                             'apiVersion': f'{objGroup}/{objVersion}',
                                             'kind': objKind,
                                             'metadata': {'name': objName},
                                             'spec': {'data': encoded_string[chunk_pos * step:(chunk_pos + 1) * step].decode("utf-8")}}
                                           )
        if not isCreated:
            print(f"backup_job: sds-replicated-volume-{backup_type}-{backup_date}-backup-{chunk_pos} creation failure")
            return False


    current_backup_objects = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group=objGroup,
                                                                                     version=objVersion,
                                                                                     plural=objKindPlural)
    regex = re.compile(f'sds-replicated-volume-{backup_type}-{backup_date}-backup-')
    for item in current_backup_objects['items']:
        if regex.match(item.get('metadata', {}).get('name', '')):
            kubernetes.client.CustomObjectsApi().patch_cluster_custom_object(
                group=objGroup,
                version=objVersion,
                plural=objKindPlural,
                name=item['metadata']['name'],
                body={"metadata": {"labels": labels}})

    for root, dirs, files in os.walk(temp_path, topdown=False):
        for name in files:
            os.remove(os.path.join(root, name))
        for name in dirs:
            os.rmdir(os.path.join(root, name))

    return True


if __name__ == "__main__":
    full_completed_list = []
    backup_list = []

    parser = argparse.ArgumentParser("backup")
    parser.add_argument("-r", "--retentionCount", help="Retention days count", type=int)
    args = parser.parse_args()
    retention = args.retentionCount or 7

    kubernetes.config.load_incluster_config()

    date_time = datetime.now().strftime("%Y%m%d%H%M%S")
    success = create_backup(backup_date=date_time, labels={srvBackupLabelKey: f'{date_time}', "completed": "true"})
    if not success:
        print(f"backup_job: creation failure")
        exit(1)

    current_backup_objects = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group=objGroup,
                                                                                     version=objVersion,
                                                                                     plural=objKindPlural)
    for item in current_backup_objects['items']:
        backup_list.append(item['metadata']['name'])
        if re.search(f'^sds-replicated-volume-{backup_type}-\\d+-backup-0$', item['metadata']['name']):
            full_completed_list.append(item['metadata']['name'])
    completed_list = full_completed_list[-retention:]
    for backup_name in backup_list:
        if not re.search(f'^sds-replicated-volume-{backup_type}-\\d+-backup-', backup_name):
            continue
        backup_cut_name = re.search(f'^sds-replicated-volume-{backup_type}-\\d+-backup-', backup_name).group(0)
        if f'{backup_cut_name}0' in completed_list:
             continue
        kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group=objGroup,
                                                                          version=objVersion,
                                                                          plural=objKindPlural,
                                                                          name=backup_name)


