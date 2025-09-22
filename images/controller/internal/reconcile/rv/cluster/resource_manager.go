package cluster

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

type ResourceManager struct {
	cl        NodeRVRClient
	portRange DRBDPortRange
	nodes     map[string]*nodeResources
}

type nodeResources struct {
	usedPorts  map[uint]struct{}
	usedMinors map[uint]struct{}
}

var _ PortManager = &ResourceManager{}

func NewResourceManager(cl NodeRVRClient, portRange DRBDPortRange) *ResourceManager {
	return &ResourceManager{
		cl:        cl,
		portRange: portRange,
	}
}

func (m *ResourceManager) ReserveNodeMinor(ctx context.Context, nodeName string) (uint, error) {
	node, err := m.initNodeResources(ctx, nodeName)
	if err != nil {
		return 0, err
	}

	// minors
	freeMinor, err := findLowestUnusedInRange(node.usedMinors, 0, 1048576)
	if err != nil {
		return 0,
			fmt.Errorf(
				"unable to find free minor on node %s: %w",
				nodeName, err,
			)
	}

	node.usedMinors[freeMinor] = struct{}{}

	return freeMinor, nil
}

func (m *ResourceManager) ReserveNodePort(ctx context.Context, nodeName string) (uint, error) {
	node, err := m.initNodeResources(ctx, nodeName)
	if err != nil {
		return 0, err
	}

	portMin, portMax := m.portRange.PortMinMax()

	freePort, err := findLowestUnusedInRange(node.usedPorts, portMin, portMax)
	if err != nil {
		return 0,
			fmt.Errorf("unable to find free port on node %s: %w", nodeName, err)
	}

	node.usedPorts[freePort] = struct{}{}

	return freePort, nil
}

func (m *ResourceManager) initNodeResources(ctx context.Context, nodeName string) (*nodeResources, error) {
	r, ok := m.nodes[nodeName]
	if ok {
		return r, nil
	}

	rvrs, err := m.cl.ByNodeName(ctx, nodeName)
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

	m.nodes[nodeName] = r

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
