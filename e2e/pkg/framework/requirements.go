/*
Copyright 2026 Flant JSC

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

package framework

import (
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
)

// registerRequirementsTransformer registers a NodeArgsTransformer that
// injects the default Req:ControlPlane:New label on every It node that
// does not already have a Req:ControlPlane:* label (from own args or
// inherited from parent containers).
func registerRequirementsTransformer() {
	AddTreeConstructionNodeArgsTransformer(
		func(nodeType types.NodeType, _ Offset, text string, args []any) (string, []any, []error) {
			if !nodeType.Is(types.NodeTypeIt) {
				return text, args, nil
			}

			if hasControlPlaneLabel(args) {
				return text, args, nil
			}

			for _, label := range CurrentTreeConstructionNodeReport().Labels() {
				if strings.HasPrefix(label, require.LabelCPPrefix) {
					return text, args, nil
				}
			}

			args = append(args, Label(require.LabelCPNew))
			return text, args, nil
		},
	)
}

// hasControlPlaneLabel reports whether args contain a Labels value with
// a Req:ControlPlane:* entry.
func hasControlPlaneLabel(args []any) bool {
	for _, arg := range args {
		if labels, ok := arg.(Labels); ok {
			for _, l := range labels {
				if strings.HasPrefix(l, require.LabelCPPrefix) {
					return true
				}
			}
		}
	}
	return false
}

// enforceRequirements reads Req:* labels from the current spec report
// and skips the spec if the cluster does not satisfy them.
func enforceRequirements(f *Framework) {
	for _, label := range CurrentSpecReport().Labels() {
		switch {
		case label == require.LabelCPNew:
			if f.controlPlane != ControlPlaneNew {
				Skip(fmt.Sprintf("requires new control plane, cluster has %v", f.controlPlane))
			}
		case label == require.LabelCPOld:
			if f.controlPlane != ControlPlaneOld {
				Skip(fmt.Sprintf("requires old control plane, cluster has %v", f.controlPlane))
			}
		case label == require.LabelCPAny:
			// no check
		case strings.HasPrefix(label, require.LabelMinNodes):
			enforceMinNodes(f, label)
		}
	}
}

// enforceMinNodes parses a Req:MinNodes:<diskful>:<extra>:<poolType> label
// and skips the spec if the cluster does not satisfy the requirements.
func enforceMinNodes(f *Framework, label string) {
	s := strings.TrimPrefix(label, require.LabelMinNodes)
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		Fail(fmt.Sprintf("invalid %s label: %q (expected format: <diskful>:<extra>:<poolType>)", require.LabelMinNodes, label))
	}

	diskful, err := strconv.Atoi(parts[0])
	if err != nil {
		Fail(fmt.Sprintf("invalid %s label: cannot parse diskful %q: %v", require.LabelMinNodes, parts[0], err))
	}

	extra, err := strconv.Atoi(parts[1])
	if err != nil {
		Fail(fmt.Sprintf("invalid %s label: cannot parse extra %q: %v", require.LabelMinNodes, parts[1], err))
	}

	poolType := v1alpha1.ReplicatedStoragePoolType(parts[2])
	pool := f.Discovery.From(poolType)

	total := diskful + extra
	usableNodes := pool.UsableNodeCount()
	if usableNodes < total {
		Skip(fmt.Sprintf("requires %d usable %s nodes (%d diskful + %d extra), cluster has %d",
			total, poolType, diskful, extra, usableNodes))
	}

	diskfulNodes := pool.UsableDiskfulNodeCount()
	if diskfulNodes < diskful {
		Skip(fmt.Sprintf("requires %d usable %s nodes with ready LVG, cluster has %d",
			diskful, poolType, diskfulNodes))
	}
}
