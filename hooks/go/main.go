package main

import (
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"

	"github.com/deckhouse/module-sdk/pkg/app"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/050-label-expiring-certs"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/060-manual-cert-renewal"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/070-generate-certs"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/080-enable-thin-provisioning"
)

func main() {
	app.Run()
}
