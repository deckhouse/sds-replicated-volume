package main

import (
	"github.com/deckhouse/module-sdk/pkg/app"
	_ "github.com/deckhouse/sds-replicated-volume/hooks/go/050-label-expiring-certs"
)

func main() {
	app.Run()
}
