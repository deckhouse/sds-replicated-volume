#!/usr/bin/env python3
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

import base64
import kubernetes
from deckhouse import hook
import os
import tarfile
import tempfile
import yaml

from re import search
from time import sleep

config = """
configVersion: v1
afterHelm: 10
"""

def main(ctx: hook.Context):
    kubernetes.config.load_incluster_config()

    # Delete old webhooks
    custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='admissionregistration.k8s.io',
                                                                                    plural='validatingwebhookconfigurations',
                                                                                    version='v1')

    for item in custom_object['items']:
        if item['metadata']['name'] in ['d8-sds-drbd-dsc-validation', 'd8-sds-drbd-sc-validation']:
            kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group='admissionregistration.k8s.io',
                                                                              plural='validatingwebhookconfigurations',
                                                                              version='v1',
                                                                              name=item['metadata']['name'])
            print(f"Deleted webhook {item['metadata']['name']}")

    # Migration of DRBDStoragePools to ReplicatedStoragePools
    try:
        custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='storage.deckhouse.io',
                                                                                        plural='drbdstoragepools',
                                                                                        version='v1alpha1')
        print(f"DRBDStoragePools to migrate: {custom_object}")
        for item in custom_object['items']:
            try:
                if item['spec'].get('lvmvolumegroups'):
                    item['spec']['lvmVolumeGroups'] = item['spec']['lvmvolumegroups']
                    del item['spec']['lvmvolumegroups']
                    for lvg in item['spec']['lvmVolumeGroups']:
                        if lvg.get('thinpoolname'):
                            lvg['thinPoolName'] = lvg['thinpoolname']
                            del lvg['thinpoolname']
                kubernetes.client.CustomObjectsApi().create_cluster_custom_object(group='storage.deckhouse.io',
                                                                                  plural='replicatedstoragepools',
                                                                                  version='v1alpha1',
                                                                                  body={
                      'apiVersion': 'storage.deckhouse.io/v1alpha1',
                      'kind': 'ReplicatedStoragePool',
                      'metadata': {'name': item['metadata']['name']},
                      'spec': item['spec']})
                print(f"ReplicatedStoragePool {item['metadata']['name']} created")

            except Exception as e:
                if e.status == 409:
                    print("ReplicatedStoragePool {item['metadata']['name']} already exists")
                else:
                    print(f"ReplicatedStoragePool {item['metadata']['name']} message while creation: {e.error_message}")
                    continue

            try:
                kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group='storage.deckhouse.io',
                                                                                  plural='drbdstoragepools',
                                                                                  version='v1alpha1',
                                                                                  name=item['metadata']['name'])
                print(f"DRBDStoragePool {item['metadata']['name']} deleted")
            except Exception as e:
                print(f"DRBDStoragePool {item['metadata']['name']} message while deletion: {e.error_message}")


    except Exception as e:
        print(f"Exception while migration of DRBDStoragePools to ReplicatedStoragePools: {e}")

    webhook_ready = False
    tries = 0
    current_pods = kubernetes.client.CoreV1Api().list_namespaced_pod('d8-sds-replicated-volume')
    while not webhook_ready and tries < 30:
        for item in current_pods.items:
            if search(r'^webhooks-', item.metadata.name):
                webhook_ready = item.status.container_statuses[0].ready
                if webhook_ready:
                    print(f'webhook {item.metadata.name} pod is ready')
                    break
                sleep(10)
                tries += 1

    # Migration of DRBDStorageClasses to ReplicatedStorageClasses
    try:
        custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='storage.deckhouse.io',
                                                                                        plural='drbdstorageclasses',
                                                                                        version='v1alpha1')
        print(f"DRBDStorageClasses to migrate: {custom_object}")
        for item in custom_object['items']:
            try:
                kubernetes.client.CustomObjectsApi().create_cluster_custom_object(
                    group='storage.deckhouse.io',
                    plural='replicatedstorageclasses',
                    version='v1alpha1',
                    body={
                    'apiVersion': 'storage.deckhouse.io/v1alpha1',
                    'kind': 'ReplicatedStorageClass',
                    'metadata': {'name': item['metadata']['name']},
                    'spec': item['spec']})
                print(f"ReplicatedStorageClass {item['metadata']['name']} created")

                kubernetes.client.CustomObjectsApi().patch_cluster_custom_object(
                    group='storage.deckhouse.io',
                    plural='drbdstorageclasses',
                    version='v1alpha1',
                    name=item['metadata']['name'],
                    body={"metadata": {"finalizers": []}}
                    )
                print(f"Removed finalizer from DRBDStorageClass {item['metadata']['name']}")

                kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group='storage.deckhouse.io',
                                                                                  plural='drbdstorageclasses',
                                                                                  version='v1alpha1',
                                                                                  name=item['metadata']['name'])
                print(f"DRBDStorageClass {item['metadata']['name']} deleted")
            except Exception as e:
                if e.status == 409:
                    print("ReplicatedStorageClass {item['metadata']['name']} already exists")
                else:
                    print(f"ReplicatedStorageClass {item['metadata']['name']} message while creation: {e.error_message}")
                    continue

            try:
                kubernetes.client.CustomObjectsApi().patch_cluster_custom_object(
                    group='storage.deckhouse.io',
                    plural='drbdstorageclasses',
                    version='v1alpha1',
                    name=item['metadata']['name'],
                    body={"metadata": {"finalizers": []}}
                    )
                print(f"Removed finalizer from DRBDStorageClass {item['metadata']['name']}")

                kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group='storage.deckhouse.io',
                                                                                  plural='drbdstorageclasses',
                                                                                  version='v1alpha1',
                                                                                  name=item['metadata']['name'])
                print(f"DRBDStorageClass {item['metadata']['name']} deleted")
            except Exception as e:
                print(f"DRBDStorageClass {item['metadata']['name']} message while deletion: {e.error_message}")

    except Exception as e:
        print(f"Exception while migration of DRBDStorageClasses to ReplicatedStorageClasses: {e}")

if __name__ == "__main__":
    hook.run(main, config=config)
