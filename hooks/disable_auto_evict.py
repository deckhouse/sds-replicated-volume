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
import yaml


config = """
configVersion: v1
afterHelm: 10
"""

def main(ctx: hook.Context):
    crd_group='internal.linstor.linbit.com'
    crd_name_plural = 'propscontainers'
    crd_version = 'v1-15-0'
    kubernetes.config.load_incluster_config()

    custom_object_list = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group=crd_group,
                                                                                         plural=crd_name_plural,
                                                                                         version=crd_version)
    for item in custom_object_list.get('items', []):
        if item.get('spec', {}).get('prop_key', None) == 'DrbdOptions/AutoEvictAllowEviction':
            if item['spec']['prop_value'] == 'True':
                kubernetes.client.CustomObjectsApi().patch_cluster_custom_object(
                    group=crd_group,
                    plural=crd_name_plural,
                    version=crd_version,
                    name=item['metadata']['name'],
                    body={"spec": {"prop_value": "False"}}
                    )
                print(f"Replaced DrbdOptions/AutoEvictAllowEviction value to False in propscontainer {item['metadata']['name']}")

if __name__ == "__main__":
    hook.run(main, config=config)