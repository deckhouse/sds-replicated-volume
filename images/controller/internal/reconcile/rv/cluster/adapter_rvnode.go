/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster

import (
	"slices"

	corev1 "k8s.io/api/core/v1"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
)

type rvNodeAdapter struct {
	RVAdapter
	nodeName, nodeIP,
	lvgName, actualVGNameOnTheNode, thinPoolName string
}

type RVNodeAdapter interface {
	RVAdapter
	NodeName() string
	NodeIP() string
	// empty if [RVNodeAdapter.Diskless]
	LVGName() string
	// empty if [RVNodeAdapter.Diskless]
	LVGActualVGNameOnTheNode() string
	// empty if [RVNodeAdapter.Diskless] or [RVAdapter.LVMType] is not "Thin"
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
) (RVNodeAdapter, error) {
	if rv == nil {
		return nil, errArgNil("rv")
	}

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

		if rv.LVMType() == "Thin" {
			res.thinPoolName = rv.ThinPoolName(lvg.Name)
		}
	}

	return res, nil
}

func (r *rvNodeAdapter) NodeIP() string {
	return r.nodeIP
}

func (r *rvNodeAdapter) NodeName() string {
	return r.nodeName
}

func (r *rvNodeAdapter) LVGName() string {
	return r.lvgName
}

func (r *rvNodeAdapter) LVGActualVGNameOnTheNode() string {
	return r.actualVGNameOnTheNode
}

func (r *rvNodeAdapter) Diskless() bool {
	return r.lvgName == ""
}

func (r *rvNodeAdapter) Primary() bool {
	return slices.Contains(r.PublishRequested(), r.nodeName)
}

func (r *rvNodeAdapter) LVGThinPoolName() string {
	return r.thinPoolName
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
