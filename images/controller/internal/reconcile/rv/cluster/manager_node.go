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
	cmaps "github.com/deckhouse/sds-replicated-volume/lib/go/common/maps"
)

type DRBDPortRange interface {
	PortMinMax() (uint, uint)
}

type NodeManager interface {
	NodeName() string
	NewNodePort() (uint, error)
	NewNodeMinor() (uint, error)
	ReserveNodeMinor(nodeMinor uint) error
	ReserveNodePort(port uint) error
}

type nodeManager struct {
	portRange  DRBDPortRange
	nodeName   string
	usedPorts  map[uint]struct{}
	usedMinors map[uint]struct{}
}

var _ NodeManager = &nodeManager{}

func NewNodeManager(portRange DRBDPortRange, nodeName string) NodeManager {
	return &nodeManager{
		nodeName:  nodeName,
		portRange: portRange,
	}
}

func (m *nodeManager) NodeName() string {
	return m.nodeName
}

func (m *nodeManager) ReserveNodeMinor(nodeMinor uint) error {
	var added bool
	if m.usedMinors, added = cmaps.SetUnique(m.usedMinors, nodeMinor, struct{}{}); !added {
		return errInvalidCluster("duplicate nodeMinor: %d", nodeMinor)
	}

	return nil
}

func (m *nodeManager) FreeNodeMinor(nodeMinor uint) {
	delete(m.usedMinors, nodeMinor)
}

func (m *nodeManager) NewNodeMinor() (nodeMinor uint, err error) {
	m.usedMinors, nodeMinor, err = cmaps.SetLowestUnused(m.usedMinors, MinNodeMinor, MaxNodeMinor)
	if err != nil {
		return 0, errInvalidCluster("unable to allocate new node device minor: %w", err)
	}

	return
}

func (m *nodeManager) ReserveNodePort(port uint) error {
	var added bool
	if m.usedPorts, added = cmaps.SetUnique(m.usedPorts, port, struct{}{}); !added {
		return errInvalidCluster("duplicate port: %d", port)
	}

	return nil
}

func (m *nodeManager) FreeNodePort(port uint) {
	delete(m.usedPorts, port)
}

func (m *nodeManager) NewNodePort() (port uint, err error) {
	portMin, portMax := m.portRange.PortMinMax()

	m.usedPorts, port, err = cmaps.SetLowestUnused(m.usedPorts, portMin, portMax)
	if err != nil {
		return 0, errInvalidCluster("unable to allocate new node port: %w", err)
	}

	return
}
