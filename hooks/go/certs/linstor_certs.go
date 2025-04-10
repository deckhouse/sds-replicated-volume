package certs

import (
	"fmt"
	"iter"

	"github.com/deckhouse/module-sdk/pkg/registry"
	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
	kcertificates "k8s.io/api/certificates/v1"
)

func RegisterLinstorCertsHook() {
	for conf := range LinstorCertConfigs() {
		registry.RegisterFunc(
			ignoreSuppressedEvents(tlscertificate.GenSelfSignedTLSConfig(conf)),
			tlscertificate.GenSelfSignedTLS(conf),
		)
	}
}

func LinstorCertConfigs() iter.Seq[tlscertificate.GenSelfSignedTLSHookConf] {
	return linstorCertConfigsFromArgs([]linstorHookArgs{
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
	})
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
				SANs:          tlscertificate.DefaultSANs(sans),
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
