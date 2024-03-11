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
import time

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
            print(f"ValidatingWebhookConfigurations to delete: {item}")
            kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group='admissionregistration.k8s.io',
                                                                              plural='validatingwebhookconfigurations',
                                                                              version='v1',
                                                                              name=item['metadata']['name'])
            print(f"Deleted webhook {item['metadata']['name']}")

    # Migration from DRBDStoragePools to ReplicatedStoragePools
    try:
        custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='storage.deckhouse.io',
                                                                                        plural='drbdstoragepools',
                                                                                        version='v1alpha1')
        print(f"DRBDStoragePools to migrate: {custom_object}")
        for item in custom_object['items']:
            if item['spec'].get('lvmvolumegroups'):
                item['spec']['lvmVolumeGroups'] = item['spec']['lvmvolumegroups']
                del item['spec']['lvmvolumegroups']
                for lvg in item['spec']['lvmVolumeGroups']:
                    if lvg.get('thinpoolname'):
                        lvg['thinPoolName'] = lvg['thinpoolname']
                        del lvg['thinpoolname']

            created = create_custom_resource('storage.deckhouse.io', 'replicatedstoragepools', 'v1alpha1', 'ReplicatedStoragePool', item['metadata']['name'], item['spec'])
            if not created:
                print(f"Skipping deletion for {item['metadata']['name']} due to creation failure")
                continue

            try:
                kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group='storage.deckhouse.io',
                                                                                  plural='drbdstoragepools',
                                                                                  version='v1alpha1',
                                                                                  name=item['metadata']['name'])
                print(f"DRBDStoragePool {item['metadata']['name']} deleted")
            except Exception as e:
                print(f"DRBDStoragePool {item['metadata']['name']} message while deletion: {e}")


    except Exception as e:
        print(f"Exception occurred during the migration process from DRBDStoragePools to ReplicatedStoragePools: {e}")

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

    # Migration from DRBDStorageClasses to ReplicatedStorageClasses
    try:
        custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='storage.deckhouse.io',
                                                                                        plural='drbdstorageclasses',
                                                                                        version='v1alpha1')
        print(f"DRBDStorageClasses to migrate: {custom_object}")
        for item in custom_object['items']:
            created = create_custom_resource('storage.deckhouse.io', 'replicatedstorageclasses', 'v1alpha1', 'ReplicatedStorageClass', item['metadata']['name'], item['spec'])
            if not created:
                print(f"Skipping deletion for {item['metadata']['name']} due to creation failure")
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
                print(f"DRBDStorageClass {item['metadata']['name']} message while deletion: {e}")

    except Exception as e:
        print(f"Exception occurred during the migration process from DRBDStorageClasses to ReplicatedStorageClasses: {e}")

def create_custom_resource(group, plural, version, kind, name, spec):
    max_attempts = 3
    delay_between_attempts = 10

    for attempt in range(max_attempts):
      try:
          kubernetes.client.CustomObjectsApi().create_cluster_custom_object(group=group,
                                                                            plural=plural,
                                                                            version=version,
                                                                            body={
                  'apiVersion': f'{group}/{version}',
                  'kind': kind,
                  'metadata': {'name': name},
                  'spec': spec})
          print(f"{kind} {name} created")
          return True
      except Exception as e:
          print(f"Attempt {attempt + 1} failed for {kind} {name} with message: {e}")
          if attempt < max_attempts - 1:
              print(f"Retrying in {delay_between_attempts} seconds...")
              time.sleep(delay_between_attempts)
          else:
              print(f"Failed to create {kind} {name} after {max_attempts} attempts")
              return False


if __name__ == "__main__":
    hook.run(main, config=config)
