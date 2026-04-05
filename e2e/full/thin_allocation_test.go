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

package full

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
)

var _ = Describe("Thin provisioning: LVM allocation after creation", Label(fw.LabelSlow), func() {
	DescribeTable("data_percent is less than 100% for all diskful replicas",
		func(ctx SpecContext, l fw.TestLayout) {
			trv := f.SetupLayout(ctx, l)

			for _, trvr := range trv.TestRVRs() {
				obj := trvr.Object()
				if obj.Spec.Type != v1alpha1.ReplicaTypeDiskful {
					continue
				}

				llvs := trvr.LLVs()
				Expect(llvs).To(HaveLen(1), "expected 1 LLV for diskful RVR %s", trvr.Name())
				llvObj := llvs[0].Object()

				var lvg snc.LVMVolumeGroup
				Expect(f.Client.Get(ctx, client.ObjectKey{Name: llvObj.Spec.LVMVolumeGroupName}, &lvg)).To(Succeed())

				lvDevPath := "/dev/" + lvg.Spec.ActualVGNameOnTheNode + "/" + llvObj.Spec.ActualLVNameOnTheNode

				res, err := f.LVM(ctx, obj.Spec.NodeName,
					"lvs", "--noheadings", "--nosuffix", "-o", "data_percent", lvDevPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.ExitCode).To(Equal(0), "lvs failed for %s on %s: %s", lvDevPath, obj.Spec.NodeName, res.Stderr)

				pctStr := strings.TrimSpace(res.Stdout)
				Expect(pctStr).NotTo(BeEmpty(), "LV %s on %s has no data_percent (not a thin LV?)", lvDevPath, obj.Spec.NodeName)

				pct, err := strconv.ParseFloat(pctStr, 64)
				Expect(err).NotTo(HaveOccurred(), "parsing data_percent %q for %s", pctStr, lvDevPath)

				fmt.Fprintf(GinkgoWriter, "[thin] node=%s lv=%s data_percent=%.2f%%\n",
					obj.Spec.NodeName, lvDevPath, pct)
				Expect(pct).To(BeNumerically("<", 100.0),
					"thin LV %s on node %s: data_percent=%.2f%%, expected <100%%",
					lvDevPath, obj.Spec.NodeName, pct)
			}
		},

		Entry("RSC(0,0) — 1D",
			fw.TestLayout{FTT: 0, GMDR: 0, Size: "2Gi"}),
		Entry("RSC(0,1) — 2D",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Size: "2Gi"}),
		Entry("RSC(1,0) — 2D+1TB",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Size: "2Gi"}),
		Entry("RSC(1,1) — 3D",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Size: "2Gi"}),
		Entry("RSC(1,2) — 4D",
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 2, Size: "2Gi"}),
		Entry("RSC(2,1) — 4D+1TB",
			SpecTimeout(3*time.Minute), require.MinNodes(5),
			fw.TestLayout{FTT: 2, GMDR: 1, Size: "2Gi"}),
		Entry("RSC(2,2) — 5D",
			SpecTimeout(3*time.Minute), require.MinNodes(5),
			fw.TestLayout{FTT: 2, GMDR: 2, Size: "2Gi"}),
	)
})
