package generatecerts

import (
	"github.com/deckhouse/sds-replicated-volume/hooks/go/certs"
	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
)

func init() {
	for cfg := range certs.LinstorCertConfigs() {
		tlscertificate.RegisterInternalTLSHookEM(cfg)
	}

	for cfg := range certs.WebhookCertConfigs() {
		tlscertificate.RegisterInternalTLSHookEM(cfg)
	}

	tlscertificate.RegisterInternalTLSHookEM(certs.SchedulerExtenderCertConfig)

	tlscertificate.RegisterInternalTLSHookEM(certs.SpaasCert)
}
