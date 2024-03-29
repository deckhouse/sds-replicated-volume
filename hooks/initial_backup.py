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

import base64
import kubernetes
from deckhouse import hook
import os
import tarfile
import tempfile
import yaml


config = """
configVersion: v1
beforeHelm: 10
"""


def create_backup(backup_type, namespace, labels={}):
    temp_path = tempfile.mkdtemp()
    backup_archive_name = f'sds-replicated-volume-{backup_type}.tar'
    backup_archive = tarfile.open(f'{temp_path}/{backup_archive_name}', "w")

    for item in kubernetes.client.ApiextensionsV1Api().list_custom_resource_definition().items:
        if item.spec.group != 'internal.linstor.linbit.com' and item.spec.group != 'storage.deckhouse.io':
            continue

        crd_name_plural = item.spec.names.plural
        version = item.spec.versions[-1]
        crd_version = version.name

        custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group=item.spec.group,
                                                                                        plural=crd_name_plural,
                                                                                        version=crd_version)

        with open(f'{temp_path}/{crd_name_plural}.yaml', 'w') as file:
            yaml.dump(custom_object, file)
        backup_archive.add(f'{temp_path}/{crd_name_plural}.yaml')

    backup_archive.close()

    with open(f'{temp_path}/{backup_archive_name}', 'rb') as backup_archive:
        encoded_string = base64.b64encode(backup_archive.read())

    step = 262144 # 256kB
    for chunk_pos in range(0, len(encoded_string) // step + 1, 1):
        body = kubernetes.client.V1Secret(
            api_version="v1",
            kind="Secret",
            metadata=kubernetes.client.V1ObjectMeta(name=f'sds-replicated-volume-{backup_type}-backup-{chunk_pos}',
                                                    namespace=namespace,
                                                    labels=labels),
            data={'filepart': encoded_string[chunk_pos * step:(chunk_pos + 1) * step].decode("utf-8")}
            )
        kubernetes.client.CoreV1Api().create_namespaced_secret(namespace=namespace, body=body)
    for root, dirs, files in os.walk(temp_path, topdown=False):
        for name in files:
            os.remove(os.path.join(root, name))
        for name in dirs:
            os.rmdir(os.path.join(root, name))


def main(ctx: hook.Context):
    namespace = 'd8-system'
    backup_type = 'initial'
    kubernetes.config.load_incluster_config()
    backup_secret_label='sds-replicated-volume.deckhouse.io/initial-backup'

    # initial_old_backup_secrets = kubernetes.client.CoreV1Api().list_namespaced_secret(namespace=namespace,
    #                                                                                  label_selector='sds-drbd.deckhouse.io/initial-backup')
    initial_backup_secrets = kubernetes.client.CoreV1Api().list_namespaced_secret(namespace=namespace,
                                                                                  label_selector=backup_secret_label)
    # if len(initial_backup_secrets.items) > 0 or len(initial_old_backup_secrets.items) > 0:
    if len(initial_backup_secrets.items) > 0:
        print('Initial backup exists')
    else:
        create_backup(backup_type=backup_type, namespace=namespace, labels={backup_secret_label: ''})


if __name__ == "__main__":
    hook.run(main, config=config)
