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
from datetime import timedelta
from OpenSSL import crypto
from typing import Callable
from lib.hooks.hook import Hook
import lib.utils as utils
import lib.certificate.certificate as certificate

PUBLIC_DOMAIN_PREFIX = "%PUBLIC_DOMAIN%://"
CLUSTER_DOMAIN_PREFIX = "%CLUSTER_DOMAIN%://"


KEY_USAGES = {
    0: "digitalSignature",
    1: "nonRepudiation",
    2: "keyEncipherment",
    3: "dataEncipherment",
    4: "keyAgreement",
    5: "keyCertSign",
    6: "cRLSign",
    7: "encipherOnly",
    8: "decipherOnly"
}

EXTENDED_KEY_USAGES = {
    0: "serverAuth",
    1: "clientAuth",
    2: "codeSigning",
    3: "emailProtection",
    4: "OCSPSigning"
}

class TlsSecret:
    def __init__(self,
                 cn: str,
                 name: str,
                 sansGenerator: Callable[[list[str]], Callable[[hook.Context], list[str]]],
                 values_path_prefix: str,
                 key_usages: list[str] = [KEY_USAGES[2], KEY_USAGES[5]],
                 extended_key_usages: list[str] = [EXTENDED_KEY_USAGES[0]]):
        self.cn = cn
        self.name = name
        self.sansGenerator = sansGenerator
        self.values_path_prefix = values_path_prefix
        self.key_usages = key_usages
        self.extended_key_usages = extended_key_usages

class GenerateCertificateHook(Hook):
    """
    Config for the hook that generates certificates.
    """
    SNAPSHOT_SECRETS_NAME = "secrets"
    SNAPSHOT_SECRETS_CHECK_NAME = "secretsCheck"

    def __init__(self, *tls_secrets: TlsSecret,
                 cn: str,
                 namespace: str,
                 module_name: str = None,
                 common_ca: bool = False,
                 before_hook_check: Callable[[hook.Context], bool] = None,
                 expire: int = 31536000,
                 key_size: int = 4096,
                 algo: str = "rsa",
                 cert_outdated_duration: timedelta = timedelta(days=30),
                 country: str = None,
                 state: str = None,
                 locality: str = None,
                 organisation_name: str = None,
                 organisational_unit_name: str = None) -> None:
        super().__init__(module_name=module_name)
        self.cn = cn
        self.tls_secrets = tls_secrets
        self.namespace = namespace
        self.common_ca = common_ca
        self.before_hook_check = before_hook_check
        self.expire = expire
        self.key_size = key_size
        self.algo = algo
        self.cert_outdated_duration = cert_outdated_duration
        self.country = country
        self.state = state
        self.locality = locality
        self.organisation_name = organisation_name
        self.organisational_unit_name = organisational_unit_name
        self.secret_names = [secret.name for secret in self.tls_secrets]
        self.queue = f"/modules/{self.module_name}/generate-certs"
        """
        :param module_name: Module name
        :type module_name: :py:class:`str`

        :param cn: Certificate common Name. often it is module name
        :type cn: :py:class:`str`

        :param sansGenerator: Function which returns list of domain to include into cert. Use default_sans
        :type sansGenerator: :py:class:`function`

        :param namespace: Namespace for TLS secret.
        :type namespace: :py:class:`str`

        :param tls_secret_name: TLS secret name. 
        Secret must be TLS secret type https://kubernetes.io/docs/concepts/configuration/secret/#tls-secrets.
        CA certificate MUST set to ca.crt key.
        :type tls_secret_name: :py:class:`str`

        :param values_path_prefix: Prefix full path to store CA certificate TLS private key and cert.
	    full paths will be
	    values_path_prefix + .`ca`  - CA certificate
	    values_path_prefix + .`crt` - TLS private key
	    values_path_prefix + .`key` - TLS certificate
	    Example: values_path_prefix =  'virtualization.internal.dvcrCert'
	    Data in values store as plain text
        :type values_path_prefix: :py:class:`str`

        :param key_usages: Optional. key_usages specifies valid usage contexts for keys.
        :type key_usages: :py:class:`list`

        :param extended_key_usages: Optional. extended_key_usages specifies valid usage contexts for keys.
        :type extended_key_usages: :py:class:`list`

        :param before_hook_check: Optional. Runs check function before hook execution. Function should return boolean 'continue' value
	    if return value is false - hook will stop its execution
	    if return value is true - hook will continue
        :type before_hook_check: :py:class:`function`

        :param expire: Optional. Validity period of SSL certificates.
        :type expire: :py:class:`int`

        :param key_size: Optional. Key Size.
        :type key_size: :py:class:`int`

        :param algo: Optional. Key generation algorithm. Supports only rsa and dsa.
        :type algo: :py:class:`str`

        :param cert_outdated_duration: Optional. (expire - cert_outdated_duration) is time to regenerate the certificate.
        :type cert_outdated_duration: :py:class:`timedelta`
        """

    def generate_config(self) -> dict:
        return {
            "configVersion": "v1",
            "beforeHelm": 5,
            "kubernetes": [
                {
                    "name": self.SNAPSHOT_SECRETS_NAME,
                    "apiVersion": "v1",
                    "kind": "Secret",
                    "nameSelector": {
                        "matchNames": self.secret_names
                    },
                    "namespace": {
                        "nameSelector": {
                            "matchNames": [self.namespace]
                        }
                    },
                    "includeSnapshotsFrom": [self.SNAPSHOT_SECRETS_NAME],
                    "jqFilter": '{"name": .metadata.name, "data": .data}',
                    "queue": self.queue,
                    "keepFullObjectsInMemory": False
                },
            ],
            "schedule": [
                {
                    "name": self.SNAPSHOT_SECRETS_CHECK_NAME,
                    "crontab": "42 4 * * *"
                }
            ]
        }

    def reconcile(self) -> Callable[[hook.Context], None]:
        def r(ctx: hook.Context) -> None:
            if self.before_hook_check is not None:
                passed = self.before_hook_check(ctx)
                if not passed:
                    return
                
            regenerate_all = False
            secrets_from_snaps = {}
            diff_secrets = []
            if len(ctx.snapshots.get(self.SNAPSHOT_SECRETS_NAME, [])) == 0:
                regenerate_all = True
            else:
                for snap in ctx.snapshots[self.SNAPSHOT_SECRETS_NAME]:
                    secrets_from_snaps[snap["filterResult"]["name"]] = snap["filterResult"]["data"]
                for secret in self.tls_secrets:
                    if secrets_from_snaps.get(secret.name) is None:
                        diff_secrets.append(secret.name)

            if self.common_ca and not regenerate_all:
                if len(diff_secrets) > 0:
                    regenerate_all = True
                else:
                    for secret in self.tls_secrets:
                        data = secrets_from_snaps[secret.name]
                        if self.is_outdated_ca(utils.base64_decode(data.get("ca.crt", ""))):
                            regenerate_all = True
                            break
                        sans = secret.sansGenerator(ctx)
                        if self.is_irrelevant_cert(utils.base64_decode(data.get("tls.crt", "")), sans):
                            regenerate_all = True
                            break
                            
            if regenerate_all:
                if self.common_ca:
                    ca = self.__get_ca_generator()
                    ca_crt, _ = ca.generate()
                    for secret in self.tls_secrets:
                        sans = secret.sansGenerator(ctx)
                        print(f"Generate new certififcates for secret {secret.name}.")
                        tls_data = self.generate_selfsigned_tls_data_with_ca(cn=secret.cn,
                                                                             ca=ca,
                                                                             ca_crt=ca_crt,
                                                                             sans=sans,
                                                                             key_usages=secret.key_usages,
                                                                             extended_key_usages=secret.extended_key_usages)
                        self.set_value(secret.values_path_prefix, ctx.values, tls_data)
                    return

                for secret in self.tls_secrets:
                    sans = secret.sansGenerator(ctx)
                    print(f"Generate new certififcates for secret {secret.name}.")
                    tls_data = self.generate_selfsigned_tls_data(cn=secret.cn,
                                                                 sans=sans,
                                                                 key_usages=secret.key_usages,
                                                                 extended_key_usages=secret.extended_key_usages)
                    self.set_value(secret.values_path_prefix, ctx.values, tls_data)
                return
            
            for secret in self.tls_secrets:
                data = secrets_from_snaps[secret.name]
                sans = secret.sansGenerator(ctx)
                cert_outdated = self.is_irrelevant_cert(
                    utils.base64_decode(data.get("tls.crt", "")), sans)
                
                tls_data = {}
                if cert_outdated or data.get("tls.key", "") == "":
                    print(f"Certificates from secret {secret.name} is invalid. Generate new certififcates.") 
                    tls_data = self.generate_selfsigned_tls_data(cn=secret.cn,
                                                sans=sans,
                                                key_usages=secret.key_usages,
                                                extended_key_usages=secret.extended_key_usages)
                else:
                    tls_data = {
                        "ca": data["ca.crt"],
                        "crt": data["tls.crt"],
                        "key": data["tls.key"]
                    }
                self.set_value(secret.values_path_prefix, ctx.values, tls_data)
        return r
       
    def __get_ca_generator(self) -> certificate.CACertificateGenerator:
        return certificate.CACertificateGenerator(cn=f"{self.cn}",
                         expire=self.expire,
                         key_size=self.key_size,
                         algo=self.algo)

    def generate_selfsigned_tls_data_with_ca(self,
                                             cn: str,
                                             ca: certificate.CACertificateGenerator,
                                             ca_crt: bytes,
                                             sans: list[str],
                                             key_usages: list[str],
                                             extended_key_usages: list[str]) -> dict[str, str]:
        """
        Generate self signed certificate. 
        :param cn: certificate common name.
        :param ca: Ca certificate generator.
        :type ca: :py:class:`certificate.CACertificateGenerator`
        :param ca_crt: bytes.
        :type ca_crt: :py:class:`bytes`
        :param sans: List of sans.
        :type sans: :py:class:`list`
        :param key_usages: List of key_usages.
        :type key_usages: :py:class:`list`
        :param extended_key_usages: List of extended_key_usages.
        :type extended_key_usages: :py:class:`list`
        Example: {
            "ca": "encoded in base64",
            "crt": "encoded in base64",
            "key": "encoded in base64"
        }
        :rtype: :py:class:`dict[str, str]`
        """
        cert = certificate.CertificateGenerator(cn=cn,
                                    expire=self.expire,
                                    key_size=self.key_size,
                                    algo=self.algo)
        if len(key_usages) > 0:
            key_usages = ", ".join(key_usages)
            cert.add_extension(type_name="keyUsage",
                               critical=False, value=key_usages)
        if len(extended_key_usages) > 0:
            extended_key_usages = ", ".join(extended_key_usages)
            cert.add_extension(type_name="extendedKeyUsage",
                               critical=False, value=extended_key_usages)
        crt, key = cert.with_metadata(country=self.country,
                                      state=self.state,
                                      locality=self.locality,
                                      organisation_name=self.organisation_name,
                                      organisational_unit_name=self.organisational_unit_name
                                      ).with_hosts(*sans).generate(ca_subj=ca.get_subject(),
                                                                   ca_key=ca.key)
        return {"ca": utils.base64_encode(ca_crt),
                "crt": utils.base64_encode(crt),
                "key": utils.base64_encode(key)}

    def generate_selfsigned_tls_data(self,
                                     cn: str,
                                     sans: list[str],
                                     key_usages: list[str],
                                     extended_key_usages: list[str]) -> dict[str, str]:
        """
        Generate self signed certificate. 
        :param cn: certificate common name.
        :param sans: List of sans.
        :type sans: :py:class:`list`
        :param key_usages: List of key_usages.
        :type key_usages: :py:class:`list`
        :param extended_key_usages: List of extended_key_usages.
        :type extended_key_usages: :py:class:`list`
        Example: {
            "ca": "encoded in base64",
            "crt": "encoded in base64",
            "key": "encoded in base64"
        }
        :rtype: :py:class:`dict[str, str]`
        """
        ca = self.__get_ca_generator()
        ca_crt, _ = ca.generate()
        return self.generate_selfsigned_tls_data_with_ca(cn=cn,
                                                         ca=ca,
                                                         ca_crt=ca_crt,
                                                         sans=sans,
                                                         key_usages=key_usages,
                                                         extended_key_usages=extended_key_usages)

    def is_irrelevant_cert(self, crt_data: str, sans: list) -> bool:
        """
        Check certificate duration and SANs list
        :param crt_data: Raw certificate
        :type crt_data: :py:class:`str`
        :param sans: List of sans.
        :type sans: :py:class:`list`
        :rtype: :py:class:`bool`
        """
        return certificate.is_irrelevant_cert(crt_data, sans, self.cert_outdated_duration)

    def is_outdated_ca(self, ca: str) -> bool:
        """
        Issue a new certificate if there is no CA in the secret. Without CA it is not possible to validate the certificate.
        Check CA duration.
        :param ca: Raw CA
        :type ca: :py:class:`str`
        :rtype: :py:class:`bool`
        """
        return certificate.is_outdated_ca(ca, self.cert_outdated_duration)

    def cert_renew_deadline_exceeded(self, crt: crypto.X509) -> bool:
        """
        Check certificate 
        :param crt: Certificate
        :type crt: :py:class:`crypto.X509`
        :return: 
            if timeNow > expire - cert_outdated_duration:
                return True
            return False
        :rtype: :py:class:`bool`
        """
        return certificate.cert_renew_deadline_exceeded(crt, self.cert_outdated_duration)

def default_sans(sans: list[str]) -> Callable[[hook.Context], list[str]]:
    """
    Generate list of sans for certificate
    :param sans: List of alt names.
    :type sans: :py:class:`list[str]`
    cluster_domain_san(san) to generate sans with respect of cluster domain (e.g.: "app.default.svc" with "cluster.local" value will give: app.default.svc.cluster.local

    public_domain_san(san)
    """
    def generate_sans(ctx: hook.Context) -> list[str]:
        res = ["localhost", "127.0.0.1"]
        public_domain = str(ctx.values["global"]["modules"].get(
            "publicDomainTemplate", ""))
        cluster_domain = str(
            ctx.values["global"]["discovery"].get("clusterDomain", ""))
        for san in sans:
            san.startswith(PUBLIC_DOMAIN_PREFIX)
            if san.startswith(PUBLIC_DOMAIN_PREFIX) and public_domain != "":
                san = get_public_domain_san(san, public_domain)
            elif san.startswith(CLUSTER_DOMAIN_PREFIX) and cluster_domain != "":
                san = get_cluster_domain_san(san, cluster_domain)
            res.append(san)
        return res
    return generate_sans


def cluster_domain_san(san: str) -> str:
    """
    Create template to enrich specified san with a cluster domain
    :param san: San.
    :type sans: :py:class:`str`
    """
    return CLUSTER_DOMAIN_PREFIX + san.rstrip('.')


def public_domain_san(san: str) -> str:
    """
    Create template to enrich specified san with a public domain
    :param san: San.
    :type sans: :py:class:`str`
    """
    return PUBLIC_DOMAIN_PREFIX + san.rstrip('.')


def get_public_domain_san(san: str, public_domain: str) -> str:
    return f"{san.lstrip(PUBLIC_DOMAIN_PREFIX)}.{public_domain}"


def get_cluster_domain_san(san: str, cluster_domain: str) -> str:
    return f"{san.lstrip(CLUSTER_DOMAIN_PREFIX)}.{cluster_domain}"
