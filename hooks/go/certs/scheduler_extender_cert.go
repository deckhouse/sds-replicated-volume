/*
Copyright 2022 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package certs

import (
	"fmt"

	kcertificates "k8s.io/api/certificates/v1"

	chcrt "github.com/deckhouse/module-sdk/common-hooks/tls-certificate"
	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
)

func RegisterSchedulerExtenderCertHook() {
	tlscertificate.RegisterManualTLSHookEM(SchedulerExtenderCertConfig)
}

var SchedulerExtenderCertConfig = tlscertificate.MustNewGenSelfSignedTLSGroupHookConf(
	tlscertificate.GenSelfSignedTLSHookConf{
		CN:            "linstor-scheduler-extender",
		Namespace:     ModuleNamespace,
		TLSSecretName: "linstor-scheduler-extender-https-certs",
		SANs: chcrt.DefaultSANs([]string{
			"linstor-scheduler-extender",
			fmt.Sprintf("linstor-scheduler-extender.%s", ModuleNamespace),
			fmt.Sprintf("linstor-scheduler-extender.%s.svc", ModuleNamespace),
			fmt.Sprintf("%%CLUSTER_DOMAIN%%://linstor-scheduler-extender.%s.svc", ModuleNamespace),
		}),
		FullValuesPathPrefix: fmt.Sprintf("%s.internal.customSchedulerExtenderCert", ModuleName),
		Usages: []kcertificates.KeyUsage{
			kcertificates.UsageKeyEncipherment,
			kcertificates.UsageCertSign,
			// ExtKeyUsage
			kcertificates.UsageServerAuth,
		},
		CAExpiryDuration:     DefaultCertExpiredDuration,
		CertExpiryDuration:   DefaultCertExpiredDuration,
		CertOutdatedDuration: DefaultCertOutdatedDuration,
	},
)
