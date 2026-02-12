package agent

import (
	"testing"

	. "github.com/deckhouse/sds-replicated-volume/e2e/agent/internal/suite"
	ctesting "github.com/deckhouse/sds-replicated-volume/lib/go/common/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDiamond(t *testing.T) {

	s := Suite{
		ZClient: ctesting.Zymbol[client.Client]{Provider: ProvideDefaultClient},
	}
	_ = s

	cl := s.Client.Require(t)

	// s.Client.SetProvider()

	// SymbolClient.SetProvider(t, ProvideDefaultClient)
	SymbolSelectedNodeNames.Provide(t, []string{})

	t.Error("oops")
}
