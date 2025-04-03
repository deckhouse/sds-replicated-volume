package certs

import (
	"fmt"
	"iter"
	"slices"

	"github.com/deckhouse/module-sdk/pkg/registry"
	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
	kcertificates "k8s.io/api/certificates/v1"
)

func RegisterWebhookCertsHook() {
	for conf := range WebhookCertConfigs() {
		registry.RegisterFunc(
			ignoreManualCertRenewalEvents(tlscertificate.GenSelfSignedTLSConfig(conf)),
			tlscertificate.GenSelfSignedTLS(conf),
		)
	}
}

func WebhookCertConfigs() iter.Seq[tlscertificate.GenSelfSignedTLSHookConf] {
	return webhookCertConfigsFromArgs([]webhookHookArgs{
		{
			cn:             "linstor-scheduler-admission",
			secretName:     "linstor-scheduler-admission-certs",
			valuesPropName: "webhookCert",
			additionalSANs: []string{
				"linstor-scheduler-admission",
				fmt.Sprintf("linstor-scheduler-admission.%s", ModuleNamespace),
				fmt.Sprintf("linstor-scheduler-admission.%s.svc", ModuleNamespace),
			},
		},
		{
			cn:             "webhooks",
			secretName:     "webhooks-https-certs",
			valuesPropName: "customWebhookCert",
			additionalSANs: []string{
				"webhooks",
				fmt.Sprintf("webhooks.%s", ModuleNamespace),
				fmt.Sprintf("webhooks.%s.svc", ModuleNamespace),
				fmt.Sprintf("%%CLUSTER_DOMAIN%%://webhooks.%s.svc", ModuleNamespace),
			},
		},
	})
}

type webhookHookArgs struct {
	cn             string
	secretName     string
	valuesPropName string
	additionalSANs []string
}

func webhookCertConfigsFromArgs(hookArgs []webhookHookArgs) iter.Seq[tlscertificate.GenSelfSignedTLSHookConf] {
	return func(yield func(tlscertificate.GenSelfSignedTLSHookConf) bool) {
		for _, args := range hookArgs {
			sans := slices.Clone(args.additionalSANs)

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
				CommonCACanonicalName: "linstor-scheduler-admission",
				CommonCAValuesPath: fmt.Sprintf(
					"%s.internal.webhooksCA",
					ModuleName,
				),
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

			if !yield(conf) {
				return
			}
		}
	}
}
