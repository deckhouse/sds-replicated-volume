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
from lib.module import module
from typing import Callable
import json
import os
import unittest


NAMESPACE   = "d8-sds-replicated-volume"
MODULE_NAME = "sdsReplicatedVolume"

def json_load(path: str):
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    return data

def get_dir_path() -> str:
    return os.path.dirname(os.path.abspath(__file__))
