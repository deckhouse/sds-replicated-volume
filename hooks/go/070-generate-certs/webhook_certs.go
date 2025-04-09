package generatecerts

import "github.com/deckhouse/sds-replicated-volume/hooks/go/certs"

func init() {
	certs.RegisterWebhookCertsHook()
}
