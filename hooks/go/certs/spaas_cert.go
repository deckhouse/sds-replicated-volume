package certs

import (
	"fmt"

	kcertificates "k8s.io/api/certificates/v1"

	chcrt "github.com/deckhouse/module-sdk/common-hooks/tls-certificate"
	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
)

func RegisterSpaasCertHook() {
	tlscertificate.RegisterManualTLSHookEM(SpaasCertConfig)
}

var SpaasCertConfig = tlscertificate.MustNewGenSelfSignedTLSGroupHookConf(
	tlscertificate.GenSelfSignedTLSHookConf{
		CN:            "spaas",
		Namespace:     ModuleNamespace,
		TLSSecretName: "spaas-certs",
		SANs: chcrt.DefaultSANs([]string{
			"spaas",
			fmt.Sprintf("spaas.%s", ModuleNamespace),
			fmt.Sprintf("spaas.%s.svc", ModuleNamespace),
		}),
		FullValuesPathPrefix:  fmt.Sprintf("%s.internal.spaasCert", ModuleName),
		CommonCACanonicalName: "spaas-ca",
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
