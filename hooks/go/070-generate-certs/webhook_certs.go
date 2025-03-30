package generatecerts

import (
	"fmt"

	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
	kcertificates "k8s.io/api/certificates/v1"
)

type webhookHookArgs struct {
	cn             string
	secretName     string
	valuesPropName string
	additionalSANs []string
}

func registerWebhookTLSHooks(hookArgs []webhookHookArgs) {
	for _, args := range hookArgs {
		sans := []string{
			"localhost",
			"127.0.0.1",
		}
		for _, san := range args.additionalSANs {
			sans = append(sans, san)
		}

		_ = tlscertificate.RegisterInternalTLSHookEM(
			tlscertificate.GenSelfSignedTLSHookConf{
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
				CAExpiryDuration:     Dur365d,
				CertExpiryDuration:   Dur365d,
				CertOutdatedDuration: Dur30d,
			},
		)
	}
}

func init() {
	registerWebhookTLSHooks([]webhookHookArgs{
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
