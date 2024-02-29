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


def create_backup(backup_type, namespace, labels={}):
    temp_path = tempfile.mkdtemp()
    backup_archive = tarfile.open(f'{temp_path}/linstor_{backup_type}.tar', "w")

    for item in kubernetes.client.ApiextensionsV1Api().list_custom_resource_definition().items:
        if item.spec.group != 'internal.linstor.linbit.com':
            continue

        crd_name_plural = item.spec.names.plural
        version = item.spec.versions[-1]
        crd_version = version.name

        custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='internal.linstor.linbit.com',
                                                                                        plural=crd_name_plural,
                                                                                        version=crd_version)

        with open(f'{temp_path}/{crd_name_plural}.yaml', 'w') as file:
            yaml.dump(custom_object, file)
        backup_archive.add(f'{temp_path}/{crd_name_plural}.yaml')

    backup_archive.close()

    with open(f'{temp_path}/linstor_{backup_type}.tar', 'rb') as backup_archive:
        encoded_string = base64.b64encode(backup_archive.read())

    step = 262144 # 256kB
    for chunk_pos in range(0, len(encoded_string) // step + 1, 1):
        body = kubernetes.client.V1Secret(
            api_version="v1",
            kind="Secret",
            metadata=kubernetes.client.V1ObjectMeta(name=f'linstor-{backup_type}-backup-{chunk_pos}',
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


if __name__ == "__main__":
    namespace = 'd8-sds-replicated-volume'
    full_completed_list = []
    backup_list = []

    parser = argparse.ArgumentParser("backup")
    parser.add_argument("-r", "--retentionCount", help="Retention days count", type=int)
    args = parser.parse_args()
    retention = args.retentionCount or 3

    kubernetes.config.load_incluster_config()

    date_time = datetime.now().strftime("%Y%m%d%H%M%S")
    create_backup(backup_type=date_time, namespace=namespace)
    body = kubernetes.client.V1Secret(
        api_version="v1",
        kind="Secret",
        metadata=kubernetes.client.V1ObjectMeta(name=f'linstor-{date_time}-backup-completed',
                                                namespace=namespace))
    kubernetes.client.CoreV1Api().create_namespaced_secret(namespace=namespace, body=body)

    namespace_secrets = kubernetes.client.CoreV1Api().list_namespaced_secret(namespace=namespace)
    for item in namespace_secrets.items:
        if re.search(r'^linstor-\d+-backup-\d+$', item.metadata.name):
            backup_list.append(item.metadata.name)
        if re.search(r'^linstor-\d+-backup-completed$', item.metadata.name):
            full_completed_list.append(item.metadata.name)
    completed_list = full_completed_list[-retention:]
    for secret_name in backup_list:
        secret_cutted_name = re.search(r'^linstor-\d+-backup-', secret_name).group(0)
        if f'{secret_cutted_name}completed' in completed_list:
            continue
        kubernetes.client.CoreV1Api().delete_namespaced_secret(namespace=namespace, name=secret_name)
    for secret_name in full_completed_list:
        if secret_name in completed_list:
            continue
        kubernetes.client.CoreV1Api().delete_namespaced_secret(namespace=namespace, name=secret_name)
