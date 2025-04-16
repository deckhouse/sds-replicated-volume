/*
Copyright 2025 Flant JSC

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

package tlscertificate

import (
	"context"
	"fmt"

	chcrt "github.com/deckhouse/module-sdk/common-hooks/tls-certificate"
	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/certificate"
	"github.com/deckhouse/module-sdk/pkg/registry"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GenSelfSignedTLSGroupHookConf []GenSelfSignedTLSHookConf

func MustNewGenSelfSignedTLSGroupHookConf(confs ...GenSelfSignedTLSHookConf) GenSelfSignedTLSGroupHookConf {
	res, err := NewGenSelfSignedTLSGroupHookConf(confs...)
	if err != nil {
		panic("GenSelfSignedTLSGroupHookConf is invalid")
	}
	return res
}

func NewGenSelfSignedTLSGroupHookConf(confs ...GenSelfSignedTLSHookConf) (GenSelfSignedTLSGroupHookConf, error) {
	var res GenSelfSignedTLSGroupHookConf

	if len(confs) == 0 {
		return res, fmt.Errorf("no configs")
	}

	for i := range confs {
		confs[i].validateAndApplyDefaults()

		if i == 0 {
			if confs[0].CommonCAValuesPath != "" {
				return res, fmt.Errorf("CommonCAValuesPath is not supported")
			}
			if confs[0].CommonCACanonicalName == "" {
				return res, fmt.Errorf("CommonCACanonicalName is required for a group of certs")
			}
		} else {
			// ensure all confs have same properties
			if confs[i].CommonCAValuesPath != confs[i-1].CommonCAValuesPath {
				return res, fmt.Errorf("group of certs should have the same CommonCAValuesPath")
			}
			if confs[i].CommonCACanonicalName != confs[i-1].CommonCACanonicalName {
				return res, fmt.Errorf("group of certs should have the same CommonCACanonicalName")
			}
			if confs[i].CAExpiryDuration != confs[i-1].CAExpiryDuration {
				return res, fmt.Errorf("group of certs should have the same CAExpiryDuration")
			}
		}
		res = append(res, confs[i])
	}

	return res, nil
}

func RegisterManualTLSHookEM(confs GenSelfSignedTLSGroupHookConf) bool {
	return registry.RegisterFunc(
		&pkg.HookConfig{
			OnBeforeHelm: &pkg.OrderedConfig{Order: 5},
		},
		GenManualSelfSignedTLS(confs),
	)
}

func GenManualSelfSignedTLS(confs GenSelfSignedTLSGroupHookConf) func(context.Context, *pkg.HookInput) error {
	return func(ctx context.Context, input *pkg.HookInput) error {
		k, err := input.DC.GetK8sClient()
		if err != nil {
			return fmt.Errorf("getting kclient: %w", err)
		}

		regenerate := false
		for _, conf := range confs {
			secret := &v1.Secret{}
			err := k.Get(
				ctx,
				types.NamespacedName{
					Namespace: conf.Namespace,
					Name:      conf.TLSSecretName,
				},
				secret,
			)
			if err != nil {
				if client.IgnoreNotFound(err) != nil {
					return fmt.Errorf("getting secret %s: %w", conf.TLSSecretName, err)
				}
				input.Logger.Info("secret not found, regenerate", "secretName", conf.TLSSecretName)
				regenerate = true
				break
			}

			values := chcrt.CertValues{
				CA:  string(secret.Data["ca.crt"]),
				Crt: string(secret.Data["tls.crt"]),
				Key: string(secret.Data["tls.key"]),
			}

			if values.CA == "" || values.Crt == "" || values.Key == "" {
				input.Logger.Info("secret empty, regenerate", "secretName", conf.TLSSecretName)
				regenerate = true
				break
			}

			input.Values.Set(conf.Path(), values)
		}

		if !regenerate {
			input.Logger.Info("certs already initialized")
			return nil
		}

		_, err = GenerateNewSelfSignedTLSGroup(input, confs)
		return err
	}
}

func GenerateNewSelfSignedTLSGroup(
	input *pkg.HookInput,
	confGroup GenSelfSignedTLSGroupHookConf,
) ([]*certificate.Certificate, error) {

	var res []*certificate.Certificate

	caConf := confGroup[0]

	auth, err := certificate.GenerateCA(
		caConf.CommonCACanonicalName,
		certificate.WithKeyAlgo(caConf.KeyAlgorithm),
		certificate.WithKeySize(caConf.KeySize),
		certificate.WithCAExpiry(caConf.CAExpiryDuration.String()))
	if err != nil {
		return nil, fmt.Errorf("generating ca: %w", err)
	}

	for _, conf := range confGroup {
		cert, err := GenerateNewSelfSignedTLS(
			SelfSignedCertValues{
				CA:           auth,
				CN:           conf.CN,
				CACN:         conf.CommonCACanonicalName,
				KeyAlgorithm: conf.KeyAlgorithm,
				KeySize:      conf.KeySize,
				SANs:         conf.SANs(input),
				Usages:       conf.UsagesStrings(),
				CAExpiry:     conf.CAExpiryDuration.String(),
				CertExpiry:   conf.CertExpiryDuration,
			},
		)

		if err != nil {
			return res, fmt.Errorf("generating certs: %w", err)
		}

		res = append(res, cert)

		input.Values.Set(conf.Path(), convCertToValues(cert))
	}

	input.Logger.Info("certs initialized")

	return res, nil
}
