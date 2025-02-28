package main

import (
<<<<<<< HEAD
=======
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"

>>>>>>> e37389c ([internal] fixes in CI, switch to werf v2)
	"github.com/deckhouse/module-sdk/pkg/app"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/050-label-expiring-certs"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/060-manual-cert-renewal"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/070-generate-certs"
<<<<<<< HEAD
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
=======
>>>>>>> e37389c ([internal] fixes in CI, switch to werf v2)
)

func main() {
	app.Run()
}
