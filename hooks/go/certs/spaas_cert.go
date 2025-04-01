package certs

import (
	"fmt"

	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
	kcertificates "k8s.io/api/certificates/v1"
)

var SpaasCert = tlscertificate.GenSelfSignedTLSHookConf{
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
	CAExpiryDuration:     Dur365d,
	CertExpiryDuration:   Dur365d,
	CertOutdatedDuration: Dur30d,
}
