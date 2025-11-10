package cluster2

import (
	"slices"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

type rvNodeAdapter struct {
	RVAdapter
	nodeName, nodeIP,
	lvgName, actualVGNameOnTheNode string
}

// AllowTwoPrimaries implements RVNodeAdapter.
// Subtle: this method shadows the method (RVAdapter).AllowTwoPrimaries of rvNodeAdapter.RVAdapter.
func (n *rvNodeAdapter) AllowTwoPrimaries() bool {
	panic("unimplemented")
}

// LVGThinPoolName implements RVNodeAdapter.
func (n *rvNodeAdapter) LVGThinPoolName() string {
	panic("unimplemented")
}

// LVGType implements RVNodeAdapter.
func (n *rvNodeAdapter) LVGType() string {
	panic("unimplemented")
}

// PublishRequested implements RVNodeAdapter.
// Subtle: this method shadows the method (RVAdapter).PublishRequested of rvNodeAdapter.RVAdapter.
func (n *rvNodeAdapter) PublishRequested() []string {
	panic("unimplemented")
}

// Quorum implements RVNodeAdapter.
// Subtle: this method shadows the method (RVAdapter).Quorum of rvNodeAdapter.RVAdapter.
func (n *rvNodeAdapter) Quorum() byte {
	panic("unimplemented")
}

// QuorumMinimumRedundancy implements RVNodeAdapter.
// Subtle: this method shadows the method (RVAdapter).QuorumMinimumRedundancy of rvNodeAdapter.RVAdapter.
func (n *rvNodeAdapter) QuorumMinimumRedundancy() byte {
	panic("unimplemented")
}

// RVName implements RVNodeAdapter.
// Subtle: this method shadows the method (RVAdapter).RVName of rvNodeAdapter.RVAdapter.
func (n *rvNodeAdapter) RVName() string {
	panic("unimplemented")
}

// Replicas implements RVNodeAdapter.
// Subtle: this method shadows the method (RVAdapter).Replicas of rvNodeAdapter.RVAdapter.
func (n *rvNodeAdapter) Replicas() byte {
	panic("unimplemented")
}

// SharedSecret implements RVNodeAdapter.
// Subtle: this method shadows the method (RVAdapter).SharedSecret of rvNodeAdapter.RVAdapter.
func (n *rvNodeAdapter) SharedSecret() string {
	panic("unimplemented")
}

// Size implements RVNodeAdapter.
// Subtle: this method shadows the method (RVAdapter).Size of rvNodeAdapter.RVAdapter.
func (n *rvNodeAdapter) Size() int {
	panic("unimplemented")
}

type RVNodeAdapter interface {
	RVAdapter
	NodeName() string
	NodeIP() string
	// empty if [RVNodeAdapter.Diskless]
	LVGName() string
	// empty if [RVNodeAdapter.Diskless]
	LVGActualVGNameOnTheNode() string
	// "Thin"/"Thick" or empty if [RVNodeAdapter.Diskless]
	LVGType() string
	// empty if [RVNodeAdapter.LVGType] is not "Thin"
	LVGThinPoolName() string
	Diskless() bool
	Primary() bool
}

var _ RVNodeAdapter = &rvNodeAdapter{}

// lvg is optional
func NewRVNodeAdapter(
	rv RVAdapter,
	node *corev1.Node,
	lvg *snc.LVMVolumeGroup,
) (*rvNodeAdapter, error) {
	if node == nil {
		return nil, errArgNil("node")
	}

	nodeHostName, nodeIP, err := nodeAddresses(node)
	if err != nil {
		return nil, err
	}

	if nodeHostName != node.Name {
		return nil,
			errInvalidNode(
				"expected node name equal hostname, got: '%s', while hostname='%s'",
				node.Name, nodeHostName,
			)
	}

	res := &rvNodeAdapter{
		RVAdapter: rv,
		nodeName:  nodeHostName,
		nodeIP:    nodeIP,
	}

	if lvg != nil {
		if lvg.Spec.Local.NodeName != node.Name {
			return nil,
				errInvalidNode(
					"expected lvg spec.local.nodeName to be the same as node name, got '%s', while node name is '%s'",
					lvg.Spec.Local.NodeName, node.Name,
				)
		}

		res.lvgName = lvg.Name
		res.actualVGNameOnTheNode = lvg.Spec.ActualVGNameOnTheNode
	}

	return res, nil
}

func (n *rvNodeAdapter) NodeIP() string {
	return n.nodeIP
}

func (n *rvNodeAdapter) NodeName() string {
	return n.nodeName
}

func (n *rvNodeAdapter) LVGName() string {
	return n.lvgName
}

func (n *rvNodeAdapter) LVGActualVGNameOnTheNode() string {
	return n.actualVGNameOnTheNode
}

func (n *rvNodeAdapter) Diskless() bool {
	return n.lvgName == ""
}

func (n *rvNodeAdapter) Primary() bool {
	return slices.Contains(n.PublishRequested(), n.nodeName)
}

func nodeAddresses(node *corev1.Node) (nodeHostName string, nodeIP string, err error) {
	for _, addr := range node.Status.Addresses {
		switch addr.Type {
		case corev1.NodeHostName:
			nodeHostName = addr.Address
		case corev1.NodeInternalIP:
			nodeIP = addr.Address
		default:
			continue
		}
		if nodeHostName != "" && nodeIP != "" {
			return
		}
	}

	if nodeHostName == "" {
		err = errInvalidNode(
			"expected node %s to have status.addresses containing item of type '%s', got none",
			node.Name, corev1.NodeHostName,
		)
	}
	if nodeIP == "" {
		err = errInvalidNode(
			"expected node %s to have status.addresses containing item of type '%s', got none",
			node.Name, corev1.NodeInternalIP,
		)
	}
	return
}
