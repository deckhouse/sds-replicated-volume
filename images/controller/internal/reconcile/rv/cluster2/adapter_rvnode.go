package cluster2

import (
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
)

type rvNodeAdapter struct {
	nodeName, nodeIP,
	lvgName, actualVGNameOnTheNode string
}

// NewNodeMinor implements RVNodeManager.
func (n *rvNodeAdapter) NewNodeMinor() (uint, error) {
	panic("unimplemented")
}

// NewNodePort implements RVNodeManager.
func (n *rvNodeAdapter) NewNodePort() (uint, error) {
	panic("unimplemented")
}

type RVNodeAdapter interface {
	NodeName() string
	NodeIP() string
	// empty if [RVNodeAdapter.Diskless]
	LVGName() string
	// empty if [RVNodeAdapter.Diskless]
	LVGActualVGNameOnTheNode() string
	Diskless() bool
}

var _ RVNodeAdapter = &rvNodeAdapter{}

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

// lvg is optional
func newRVNodeAdapter(
	rv *v1alpha2.ReplicatedVolume,
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
		nodeName: nodeHostName,
		nodeIP:   nodeIP,
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
