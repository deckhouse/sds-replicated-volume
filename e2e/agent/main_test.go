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

package agent

import (
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/internal/suite"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

func TestDRBDResource(t *testing.T) {
	e := envtesting.New(t)

	cl := suite.DiscoverClient(e)
	cs := suite.DiscoverClientset(e)

	var podLogOpts kubetesting.PodLogMonitorOptions
	e.Options(&podLogOpts)
	podsLogs := kubetesting.SetupPodsLogWatcher(e, cl, cs, podLogOpts)
	kubetesting.SetupErrorLogsWatcher(e, podsLogs)

	cluster := suite.DiscoverCluster(e, cl)

	e.Run("R1", func(e envtesting.E) {
		drbdr, _ := suite.SetupDisklessToDiskfulReplica(e, cl, cluster, "r1", 0)

		e.Run("MaintenanceMode", func(e envtesting.E) {
			suite.SetupMaintenanceMode(e, cl, drbdr)
		})

		e.Run("StateDown", func(e envtesting.E) {
			suite.SetupStateDown(e, cl, drbdr)
		})
	})

	for _, tc := range []struct {
		name   string
		prefix string
		n      int
	}{
		{"R2", "r2", 2},
		{"R3", "r3", 3},
		{"R4", "r4", 4},
	} {
		e.Run(tc.name, func(e envtesting.E) {
			e.Parallel()

			if len(cluster.Nodes) < tc.n {
				e.Skipf("%s requires at least %d nodes, got %d", tc.name, tc.n, len(cluster.Nodes))
			}

			drbdrs := make([]*v1alpha1.DRBDResource, tc.n)
			for i := range drbdrs {
				drbdrs[i], _ = suite.SetupDisklessToDiskfulReplica(e, cl, cluster, tc.prefix, i)
			}

			drbdrs = suite.SetupPeering(e, cl, drbdrs)
			suite.SetupInitialSync(e, cl, drbdrs)

			e.Run("PromotePrimary", func(e envtesting.E) {
				suite.SetupPromotePrimary(e, cl, drbdrs[0])

				e.Run("DemoteToSecondary", func(e envtesting.E) {
					suite.SetupDemoteToSecondary(e, cl, drbdrs[0])
				})
			})

			if tc.n == 2 {
				e.Run("RemovePeer", func(e envtesting.E) {
					suite.SetupRemovePeer(e, cl, drbdrs[0])
				})
			}
		})
	}
}
