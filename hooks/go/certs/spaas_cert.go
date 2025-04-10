package certs

import (
	"fmt"

	kcertificates "k8s.io/api/certificates/v1"

	"github.com/deckhouse/module-sdk/pkg/registry"
	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
)

func RegisterSpaasCertHook() {
	registry.RegisterFunc(
		ignoreSuppressedEvents(tlscertificate.GenSelfSignedTLSConfig(SpaasCertConfig)),
		tlscertificate.GenSelfSignedTLS(SpaasCertConfig),
	)
}

var SpaasCertConfig = tlscertificate.GenSelfSignedTLSHookConf{
	CN:            "spaas",
	Namespace:     ModuleNamespace,
	TLSSecretName: "spaas-certs",
	SANs: tlscertificate.DefaultSANs([]string{
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
