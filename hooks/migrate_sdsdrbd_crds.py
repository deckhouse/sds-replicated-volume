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

# This webhook ensures the migration of resources from the sds-drbd module to the sds-replicated-volume:
# - Removes old sds-drbd webhooks (if any)
# - Migrates DRBDStoragePool to ReplicatedStoragePool
# - Migrates DRBDStorageClass to ReplicatedStorageClass

def main(ctx: hook.Context):
    kubernetes.config.load_incluster_config()

    # Delete old webhooks
    custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='admissionregistration.k8s.io',
                                                                                    plural='validatingwebhookconfigurations',
                                                                                    version='v1')

    for item in custom_object['items']:
        if item['metadata']['name'] in ['d8-sds-drbd-dsc-validation', 'd8-sds-drbd-sc-validation']:
            print(f"migrate_sdsdrbd_crds.py: ValidatingWebhookConfigurations to delete: {item}")
            kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group='admissionregistration.k8s.io',
                                                                              plural='validatingwebhookconfigurations',
                                                                              version='v1',
                                                                              name=item['metadata']['name'])
            print(f"migrate_sdsdrbd_crds.py: Deleted webhook {item['metadata']['name']}")

    # Migration from DRBDStoragePools to ReplicatedStoragePools
    try:
        custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='storage.deckhouse.io',
                                                                                        plural='drbdstoragepools',
                                                                                        version='v1alpha1')
        print(f"migrate_sdsdrbd_crds.py: DRBDStoragePools to migrate: {custom_object}")
        for item in custom_object['items']:
            if item['spec'].get('lvmvolumegroups'):
                item['spec']['lvmVolumeGroups'] = item['spec']['lvmvolumegroups']
                del item['spec']['lvmvolumegroups']
                for lvg in item['spec']['lvmVolumeGroups']:
                    if lvg.get('thinpoolname'):
                        lvg['thinPoolName'] = lvg['thinpoolname']
                        del lvg['thinpoolname']

            isCreated = create_custom_resource('storage.deckhouse.io', 'replicatedstoragepools', 'v1alpha1', 'ReplicatedStoragePool', item['metadata']['name'], item['spec'])
            if not isCreated:
                print(f"migrate_sdsdrbd_crds.py: Skipping deletion for {item['metadata']['name']} due to creation failure")
                continue

            try:
                kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group='storage.deckhouse.io',
                                                                                  plural='drbdstoragepools',
                                                                                  version='v1alpha1',
                                                                                  name=item['metadata']['name'])
                print(f"migrate_sdsdrbd_crds.py: DRBDStoragePool {item['metadata']['name']} deleted")
            except Exception as e:
                print(f"migrate_sdsdrbd_crds.py: DRBDStoragePool {item['metadata']['name']} deletion error: {e}")


    except Exception as e:
        print(f"migrate_sdsdrbd_crds.py: Exception occurred during the migration process from DRBDStoragePools to ReplicatedStoragePools: {e}")

    webhook_pod_ready = False
    tries = 0
    current_pods = kubernetes.client.CoreV1Api().list_namespaced_pod('d8-sds-replicated-volume')
    while not webhook_pod_ready and tries < 30:
        print(f"migrate_sdsdrbd_crds.py: Searching webhook pod (Try number {tries})")
        for item in current_pods.items:
            if search(r'^webhooks-', item.metadata.name) and item.status and item.status.container_statuses:
                webhook_pod_ready = item.status.container_statuses[0].ready
        if webhook_pod_ready:
            print(f'migrate_sdsdrbd_crds.py: webhook {item.metadata.name} pod is ready')
            break
        sleep(10)
        tries += 1

    # Migration from DRBDStorageClasses to ReplicatedStorageClasses
    try:
        custom_object = kubernetes.client.CustomObjectsApi().list_cluster_custom_object(group='storage.deckhouse.io',
                                                                                        plural='drbdstorageclasses',
                                                                                        version='v1alpha1')
        print(f"migrate_sdsdrbd_crds.py: DRBDStorageClasses to migrate: {custom_object}")
        for item in custom_object['items']:
            if not item['spec'].get('topology'):
                item['spec']['topology'] ='Ignored'
            isCreated = create_custom_resource('storage.deckhouse.io', 'replicatedstorageclasses', 'v1alpha1', 'ReplicatedStorageClass', item['metadata']['name'], item['spec'])
            if not isCreated:
                print(f"migrate_sdsdrbd_crds.py: Skipping deletion for {item['metadata']['name']} due to creation failure")
                continue

            try:
                kubernetes.client.CustomObjectsApi().patch_cluster_custom_object(
                    group='storage.deckhouse.io',
                    plural='drbdstorageclasses',
                    version='v1alpha1',
                    name=item['metadata']['name'],
                    body={"metadata": {"finalizers": []}}
                    )
                print(f"migrate_sdsdrbd_crds.py: Removed finalizer from DRBDStorageClass {item['metadata']['name']}")

                kubernetes.client.CustomObjectsApi().delete_cluster_custom_object(group='storage.deckhouse.io',
                                                                                  plural='drbdstorageclasses',
                                                                                  version='v1alpha1',
                                                                                  name=item['metadata']['name'])
                print(f"migrate_sdsdrbd_crds.py: DRBDStorageClass {item['metadata']['name']} deleted")
            except Exception as e:
                print(f"migrate_sdsdrbd_crds.py: DRBDStorageClass {item['metadata']['name']} deletion error: {e}")

    except Exception as e:
        print(f"migrate_sdsdrbd_crds.py: Exception occurred during the migration process from DRBDStorageClasses to ReplicatedStorageClasses: {e}")

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
          print(f"migrate_sdsdrbd_crds.py: {kind} {name} created")
          return True
      except Exception as e:
          print(f"migrate_sdsdrbd_crds.py: Attempt {attempt + 1} failed for {kind} {name} with message: {e}")
          if attempt < max_attempts - 1:
              print(f"migrate_sdsdrbd_crds.py: Retrying in {delay_between_attempts} seconds...")
              time.sleep(delay_between_attempts)
          else:
              print(f"migrate_sdsdrbd_crds.py: Failed to create {kind} {name} after {max_attempts} attempts")
              return False


if __name__ == "__main__":
    hook.run(main, config=config)
