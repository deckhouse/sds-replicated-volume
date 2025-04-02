package certs

import (
	"fmt"

	"github.com/deckhouse/module-sdk/pkg/registry"
	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
	kcertificates "k8s.io/api/certificates/v1"
)

func RegisterSpaasCertHook() {
	registry.RegisterFunc(
		ignoreManualCertRenewalEvents(tlscertificate.GenSelfSignedTLSConfig(SpaasCertConfig)),
		tlscertificate.GenSelfSignedTLS(SpaasCertConfig),
	)
}

var SpaasCertConfig = tlscertificate.GenSelfSignedTLSHookConf{
	CN:            "spaas",
	Namespace:     ModuleNamespace,
	TLSSecretName: "spaas-certs",
	SANs: tlscertificate.DefaultSANs([]string{
		"localhost",
		"127.0.0.1",
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
}
