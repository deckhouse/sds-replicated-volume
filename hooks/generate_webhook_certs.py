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


from lib.hooks.internal_tls import GenerateCertificateHook, TlsSecret, default_sans
from lib.module import values as module_values
from deckhouse import hook
from typing import Callable
import common

def main():
    hook = GenerateCertificateHook(
        TlsSecret(
            cn="linstor-scheduler-admission",
            name="linstor-scheduler-admission-certs",
            sansGenerator=default_sans([
                "linstor-scheduler-admission",
                f"linstor-scheduler-admission.{common.NAMESPACE}",
                f"linstor-scheduler-admission.{common.NAMESPACE}.svc"]),
            values_path_prefix=f"{common.MODULE_NAME}.internal.webhookCert"
            ),
        TlsSecret(
            cn="webhooks",
            name="webhooks-https-certs",
            sansGenerator=default_sans([
                "webhooks",
                f"webhooks.{common.NAMESPACE}",
                f"webhooks.{common.NAMESPACE}.svc",
                f"%CLUSTER_DOMAIN%://webhooks.{common.NAMESPACE}.svc",
                ]),
            values_path_prefix=f"{common.MODULE_NAME}.internal.customWebhookCert"
            ),
        cn="linstor-scheduler-admission",
        common_ca=True,
        namespace=common.NAMESPACE)

    hook.run()

if __name__ == "__main__":
    main()
