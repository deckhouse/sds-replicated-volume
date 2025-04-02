package certs

import (
	"fmt"

	"github.com/deckhouse/module-sdk/pkg/registry"
	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
	kcertificates "k8s.io/api/certificates/v1"
)

func RegisterSchedulerExtenderCertHook() {
	registry.RegisterFunc(
		ignoreManualCertRenewalEvents(tlscertificate.GenSelfSignedTLSConfig(SchedulerExtenderCertConfig)),
		tlscertificate.GenSelfSignedTLS(SchedulerExtenderCertConfig),
	)
}

var SchedulerExtenderCertConfig = tlscertificate.GenSelfSignedTLSHookConf{
	CN:            "linstor-scheduler-extender",
	Namespace:     ModuleNamespace,
	TLSSecretName: "linstor-scheduler-extender-https-certs",
	SANs: tlscertificate.DefaultSANs([]string{
		"localhost",
		"127.0.0.1",
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
}
