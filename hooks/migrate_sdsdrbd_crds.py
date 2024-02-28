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


config = """
configVersion: v1
afterHelm: 10
"""

def main(ctx: hook.Context):
    kubernetes.config.load_incluster_config()

    custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='storage.deckhouse.io',
                                                                                    plural='drbdstorageclasses',
                                                                                    version='v1alpha1')

    for item in custom_object['items']:
        try:
            kubernetes.client.CustomObjectsApi().create_cluster_custom_object(group='storage.deckhouse.io',
                                                                                    plural='replicatedstorageclasses',
                                                                                    version='v1alpha1',
                                                                                    body={
                'apiVersion': 'storage.deckhouse.io/v1alpha1',
                'kind': 'ReplicatedStorageClass',
                'metadata': {'name': item['metadata']['name']},
                'spec': item['spec']})
            print(f"ReplicatedStorageClass {item['metadata']['name']} created")
        except Exception as e:
            print(f"ReplicatedStorageClass {item['metadata']['name']} message while creation: {e}")

    custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='storage.deckhouse.io',
                                                                                    plural='drbdstoragepools',
                                                                                    version='v1alpha1')

    for item in custom_object['items']:
        try:
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
            print(f"ReplicatedStoragePool {item['metadata']['name']} message while creation: {e}")

if __name__ == "__main__":
    hook.run(main, config=config)