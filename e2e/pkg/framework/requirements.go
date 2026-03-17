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
			s := strings.TrimPrefix(label, require.LabelMinNodes)
			n, err := strconv.Atoi(s)
			if err != nil {
				Fail(fmt.Sprintf("invalid %s label: %q", require.LabelMinNodes, label))
			}
			nodeCount := len(f.Discovery.EligibleNodes())
			if nodeCount < n {
				Skip(fmt.Sprintf("requires at least %d nodes, cluster has %d", n, nodeCount))
			}
		}
	}
}
