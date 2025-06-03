package manualcertrenewal

import (
	"context"
	"os"
	"testing"

	"github.com/deckhouse/deckhouse/pkg/log"
	"github.com/deckhouse/module-sdk/pkg"
)

func TestManualCertRenewal(t *testing.T) {
	devMode = true
	os.Setenv("LOG_LEVEL", "INFO")

	err := manualCertRenewal(context.Background(), &pkg.HookInput{
		Logger: log.Default(),
	})

	if err != nil {
		t.Fatal(err)
	}
}
