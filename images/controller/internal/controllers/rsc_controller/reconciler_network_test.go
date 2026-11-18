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

package rsccontroller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("SDN pool diagnostics propagation", func() {
	It("copies network failure and partial exclusion messages unchanged", func() {
		pool := &v1alpha1.ReplicatedStoragePool{}
		class := &v1alpha1.ReplicatedStorageClass{}
		for _, status := range []metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionTrue} {
			obju.SetStatusCondition(pool, metav1.Condition{Type: v1alpha1.ReplicatedStoragePoolCondReadyType, Status: status, Reason: v1alpha1.ReplicatedStoragePoolCondReadyReasonNoNodesWithRequiredSystemNetworks, Message: "node-a: storage (SDN unavailable)"})
			Expect(applyStoragePoolReadyCondFromRSP(class, pool)).To(BeTrue())
			condition := obju.GetStatusCondition(class, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType)
			Expect(condition.Status).To(Equal(status))
			Expect(condition.Reason).To(Equal("NoNodesWithRequiredSystemNetworks"))
			Expect(condition.Message).To(Equal("node-a: storage (SDN unavailable)"))
		}
	})
})
