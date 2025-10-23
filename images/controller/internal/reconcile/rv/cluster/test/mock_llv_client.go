package clustertest

import (
	"context"
	"maps"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
)

type LLVPhysicalKey struct {
	nodeName, actualLVNameOnTheNode string
}

type MockLLVClient struct {
	llvs map[LLVPhysicalKey]*snc.LVMLogicalVolume
}

func NewMockLLVClient(llvs map[LLVPhysicalKey]*snc.LVMLogicalVolume) *MockLLVClient {
	res := &MockLLVClient{llvs: maps.Clone(llvs)}
	return res
}

func (m *MockLLVClient) ByActualLVNameOnTheNode(
	ctx context.Context,
	nodeName string,
	actualLVNameOnTheNode string,
) (*snc.LVMLogicalVolume, error) {
	return m.llvs[LLVPhysicalKey{nodeName, actualLVNameOnTheNode}], nil
}

var _ cluster.LLVClient = &MockLLVClient{}
