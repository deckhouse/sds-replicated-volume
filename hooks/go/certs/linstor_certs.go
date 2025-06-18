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
	"iter"
	"slices"

	kcertificates "k8s.io/api/certificates/v1"

	chcrt "github.com/deckhouse/module-sdk/common-hooks/tls-certificate"
	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
)

func RegisterLinstorCertsHook() {
	tlscertificate.RegisterManualTLSHookEM(LinstorCertConfigs())
}

func LinstorCertConfigs() tlscertificate.GenSelfSignedTLSGroupHookConf {
	return tlscertificate.MustNewGenSelfSignedTLSGroupHookConf(
		slices.Collect(
			linstorCertConfigsFromArgs(
				[]linstorHookArgs{
					{
						cn:             "linstor-controller",
						secretName:     "linstor-controller-https-cert",
						valuesPropName: "httpsControllerCert",
						addLinstorSANs: true,
					},
					{
						cn:             "linstor-client",
						secretName:     "linstor-client-https-cert",
						valuesPropName: "httpsClientCert",
					},
					{
						cn:             "linstor-controller",
						secretName:     "linstor-controller-ssl-cert",
						valuesPropName: "sslControllerCert",
						addLinstorSANs: true,
					},
					{
						cn:             "linstor-node",
						secretName:     "linstor-node-ssl-cert",
						valuesPropName: "sslNodeCert",
					},
				},
			),
		)...,
	)
}

type linstorHookArgs struct {
	cn             string
	secretName     string
	valuesPropName string
	addLinstorSANs bool
}

func linstorCertConfigsFromArgs(hookArgs []linstorHookArgs) iter.Seq[tlscertificate.GenSelfSignedTLSHookConf] {
	return func(yield func(tlscertificate.GenSelfSignedTLSHookConf) bool) {
		for _, args := range hookArgs {
			sans := []string{
				"localhost",
				"127.0.0.1",
			}
			if args.addLinstorSANs {
				sans = append(
					sans,
					"linstor",
					fmt.Sprintf("linstor.%s", ModuleNamespace),
					fmt.Sprintf("linstor.%s.svc", ModuleNamespace),
				)
			}

			conf := tlscertificate.GenSelfSignedTLSHookConf{
				CN:            args.cn,
				Namespace:     ModuleNamespace,
				TLSSecretName: args.secretName,
				SANs:          chcrt.DefaultSANs(sans),
				FullValuesPathPrefix: fmt.Sprintf(
					"%s.internal.%s",
					ModuleName,
					args.valuesPropName,
				),
				CommonCACanonicalName: "linstor-ca",
				Usages: []kcertificates.KeyUsage{
					kcertificates.UsageDigitalSignature,
					kcertificates.UsageKeyEncipherment,
					// ExtKeyUsage
					kcertificates.UsageServerAuth,
					kcertificates.UsageClientAuth,
				},
				CAExpiryDuration:     DefaultCertExpiredDuration,
				CertExpiryDuration:   DefaultCertExpiredDuration,
				CertOutdatedDuration: DefaultCertOutdatedDuration,
			}

			if !yield(conf) {
				return
			}
		}
	}
}
