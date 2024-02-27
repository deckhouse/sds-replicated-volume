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

from deckhouse import hook
from typing import Callable

import yaml
import hashlib

from lib.hooks.hook import Hook
from lib.module import values as module_values
import common


NODE_SNAPSHOT_NAME = "nodes"
MODULE_NAME = "sds-replicated-volume"
NODE_LABELS_TO_FOLLOW = {"storage.deckhouse.io/sds-replicated-volume-node": ""}
QUEUE = f"/modules/{MODULE_NAME}/node-label-change"

config = {
    "configVersion": "v1",
    "kubernetes": [
        {
            "name": NODE_SNAPSHOT_NAME,
            "apiVersion": "v1",
            "kind": "Node",
            "includeSnapshotsFrom": [
                NODE_SNAPSHOT_NAME
            ],
            "jqFilter": '{"uid": .metadata.uid}',
            "labelSelector": {
                "matchLabels": NODE_LABELS_TO_FOLLOW
            },
            "queue": QUEUE,
            "keepFullObjectsInMemory": False
        }
    ]
}

def main(ctx: hook.Context):
    uid_list = sorted(_['filterResult']['uid'] for _ in ctx.snapshots.get(NODE_SNAPSHOT_NAME, []))
    hash = hashlib.sha256(bytes(str(uid_list), 'UTF-8')).hexdigest()
    module_values.set_value(f"{common.MODULE_NAME}.internal.dataNodesChecksum", ctx.values, hash)

if __name__ == "__main__":
    if isinstance(config, dict):
        conf = yaml.dump(config)
    hook.run(main, config=conf)
