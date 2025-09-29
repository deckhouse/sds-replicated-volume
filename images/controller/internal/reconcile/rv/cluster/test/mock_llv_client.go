package clustertest

import (
	"context"
	"maps"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
)

type LLVPhysicalKey struct {
	nodeName, actualVGNameOnTheNode, actualLVNameOnTheNode string
}

type MockLLVClient struct {
	llvs map[LLVPhysicalKey]*snc.LVMLogicalVolume
}

func NewMockLLVClient(llvs map[LLVPhysicalKey]*snc.LVMLogicalVolume) *MockLLVClient {
	res := &MockLLVClient{llvs: maps.Clone(llvs)}
	return res
}

func (m *MockLLVClient) ByActualNamesOnTheNode(
	ctx context.Context,
	nodeName string,
	actualVGNameOnTheNode string,
	actualLVNameOnTheNode string,
) (*snc.LVMLogicalVolume, error) {
	return m.llvs[LLVPhysicalKey{nodeName, actualVGNameOnTheNode, actualLVNameOnTheNode}], nil
}

var _ cluster.LLVClient = &MockLLVClient{}
