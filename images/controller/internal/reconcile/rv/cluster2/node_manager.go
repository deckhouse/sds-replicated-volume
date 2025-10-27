package cluster2

import (
	"context"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type NodeRVRClient interface {
	ByNodeName(ctx context.Context, nodeName string) ([]v1alpha2.ReplicatedVolumeReplica, error)
}

type DRBDPortRange interface {
	PortMinMax() (uint, uint)
}

type nodeManager struct {
	cl        NodeRVRClient
	portRange DRBDPortRange
	nodes     map[string]*nodeResources
}

type nodeResources struct {
	usedPorts  map[uint]struct{}
	usedMinors map[uint]struct{}
}

var _ NodeManager = &nodeManager{}

func NewNodeManager(cl NodeRVRClient, portRange DRBDPortRange) *nodeManager {
	return &nodeManager{
		cl:        cl,
		portRange: portRange,
	}
}

func (m *nodeManager) ReserveNodeMinor(ctx context.Context, node NodeAdapter) (uint, error) {
	if ctx == nil {
		return 0, errArgNil("ctx")
	}
	if node == nil {
		return 0, errArgNil("node")
	}

	nodeRes, err := m.initNodeResources(ctx, node)
	if err != nil {
		return 0, err
	}

	// minors
	freeMinor, err := findLowestUnusedInRange(nodeRes.usedMinors, 0, 1048576)
	if err != nil {
		return 0,
			fmt.Errorf(
				"unable to find free minor on node %s: %w",
				node.NodeName(), err,
			)
	}

	nodeRes.usedMinors[freeMinor] = struct{}{}

	return freeMinor, nil
}

func (m *nodeManager) ReserveNodePort(ctx context.Context, node NodeAdapter) (uint, error) {
	if ctx == nil {
		return 0, errArgNil("ctx")
	}
	if node == nil {
		return 0, errArgNil("node")
	}

	nodeRes, err := m.initNodeResources(ctx, node)
	if err != nil {
		return 0, err
	}

	portMin, portMax := m.portRange.PortMinMax()

	freePort, err := findLowestUnusedInRange(nodeRes.usedPorts, portMin, portMax)
	if err != nil {
		return 0,
			fmt.Errorf("unable to find free port on node %s: %w", node.NodeName(), err)
	}

	nodeRes.usedPorts[freePort] = struct{}{}

	return freePort, nil
}

func (m *nodeManager) initNodeResources(ctx context.Context, node NodeAdapter) (*nodeResources, error) {
	r, ok := m.nodes[node.NodeName()]
	if ok {
		return r, nil
	}

	rvrs, err := m.cl.ByNodeName(ctx, node.NodeName())
	if err != nil {
		return nil, err
	}

	r = &nodeResources{
		usedPorts:  map[uint]struct{}{},
		usedMinors: map[uint]struct{}{},
	}
	for i := range rvrs {
		r.usedPorts[rvrs[i].Spec.NodeAddress.Port] = struct{}{}
		for _, v := range rvrs[i].Spec.Volumes {
			r.usedMinors[v.Device] = struct{}{}
		}
	}

	if m.nodes == nil {
		m.nodes = make(map[string]*nodeResources, 1)
	}

	m.nodes[node.NodeName()] = r

	return r, nil
}

func findLowestUnusedInRange(used map[uint]struct{}, minVal, maxVal uint) (uint, error) {
	for i := minVal; i <= maxVal; i++ {
		if _, ok := used[i]; !ok {
			return i, nil
		}
	}
	return 0, fmt.Errorf("unable to find a free number in range [%d;%d]", minVal, maxVal)
}
