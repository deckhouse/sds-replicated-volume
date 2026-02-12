package suite

import (
	"testing"

	agentClient "github.com/deckhouse/sds-replicated-volume/e2e/agent/internal/client"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ProvideDefaultClient(t *testing.T) client.Client {
	cl, err := agentClient.NewClient()
	if err != nil {
		t.Fatal(err)
	}

	// no cleanup for client needed

	return cl
}
