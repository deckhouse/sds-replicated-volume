package main

import (
	"github.com/deckhouse/module-sdk/pkg/app"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/050-label-expiring-certs"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/060-manual-cert-renewal"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/070-generate-certs"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

func main() {
	app.Run()
}
