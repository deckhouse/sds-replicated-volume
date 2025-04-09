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


from lib.hooks.internal_tls import GenerateCertificateHook, TlsSecret, default_sans, KEY_USAGES, EXTENDED_KEY_USAGES
from lib.module import values as module_values
from deckhouse import hook
from typing import Callable
import common

def main():
    hook = GenerateCertificateHook(
        TlsSecret(
            key_usages=[KEY_USAGES[0], KEY_USAGES[2]],
            extended_key_usages=[EXTENDED_KEY_USAGES[0],EXTENDED_KEY_USAGES[1]],
            cn='linstor-controller',
            name="linstor-controller-https-cert",
            sansGenerator=default_sans([
                "linstor",
                f"linstor.{common.NAMESPACE}",
                f"linstor.{common.NAMESPACE}.svc"]),
            values_path_prefix=f"{common.MODULE_NAME}.internal.httpsControllerCert"
            ),
        TlsSecret(
            key_usages=[KEY_USAGES[0], KEY_USAGES[2]],
            extended_key_usages=[EXTENDED_KEY_USAGES[0],EXTENDED_KEY_USAGES[1]],
            cn='linstor-client',
            name="linstor-client-https-cert",
            sansGenerator=default_sans([]),
            values_path_prefix=f"{common.MODULE_NAME}.internal.httpsClientCert"
            ),
        TlsSecret(
            key_usages=[KEY_USAGES[0], KEY_USAGES[2]],
            extended_key_usages=[EXTENDED_KEY_USAGES[0],EXTENDED_KEY_USAGES[1]],
            cn='linstor-controller',
            name="linstor-controller-ssl-cert",
            sansGenerator=default_sans([
                "linstor",
                f"linstor.{common.NAMESPACE}",
                f"linstor.{common.NAMESPACE}.svc"]),
            values_path_prefix=f"{common.MODULE_NAME}.internal.sslControllerCert"
            ),
        TlsSecret(
            key_usages=[KEY_USAGES[0], KEY_USAGES[2]],
            extended_key_usages=[EXTENDED_KEY_USAGES[0],EXTENDED_KEY_USAGES[1]],
            cn='linstor-node',
            name="linstor-node-ssl-cert",
            sansGenerator=default_sans([]),
            values_path_prefix=f"{common.MODULE_NAME}.internal.sslNodeCert"
            ),
        cn="linstor-ca",
        common_ca=True,
        namespace=common.NAMESPACE)

    hook.run()

if __name__ == "__main__":
    print("PY HOOK")
    main()
