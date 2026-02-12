package agent

import (
	"testing"

	. "github.com/deckhouse/sds-replicated-volume/e2e/agent/internal/suite"
)

func TestDiamond(t *testing.T) {

	cl := SetupClient(t)

	nodeNames := []string{}

	nodes := SetupExistingNodes(t, cl, nodeNames)

	_ = nodes

	t.Error("oops")
}
