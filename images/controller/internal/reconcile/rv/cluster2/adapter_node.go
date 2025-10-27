package cluster2

import (
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

type nodeAdapter struct {
	nodeName, nodeIP,
	lvgName, actualVGNameOnTheNode string
}

type NodeAdapter interface {
	NodeName() string
	NodeIP() string
	LVGName() string
	LVGActualVGNameOnTheNode() string
	Diskless() bool
}

var _ NodeAdapter = &nodeAdapter{}

func (n *nodeAdapter) NodeIP() string {
	return n.nodeIP
}

func (n *nodeAdapter) NodeName() string {
	return n.nodeName
}

func (n *nodeAdapter) LVGName() string {
	return n.lvgName
}

func (n *nodeAdapter) LVGActualVGNameOnTheNode() string {
	return n.actualVGNameOnTheNode
}

func (n *nodeAdapter) Diskless() bool {
	return n.lvgName == ""
}

// lvg is optional
func newNodeAdapter(node *corev1.Node, lvg *snc.LVMVolumeGroup) (*nodeAdapter, error) {
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

	res := &nodeAdapter{
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
