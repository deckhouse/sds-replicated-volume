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

package rvrstatusconfigaddress_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestRvrStatusConfigAddress(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrStatusConfigAddress Suite")
}

// makeReady sets up an RVR to be in ready state by initializing Status and DRBD.Config with NodeId and Address
func makeReady(rvr *v1alpha1.ReplicatedVolumeReplica, _ uint, address v1alpha1.Address) {
	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha1.DRBD{}
	}

	if rvr.Status.DRBD.Config == nil {
		rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
	}

	rvr.Status.DRBD.Config.Address = &address
}

// BeReady returns a matcher that checks if an RVR is in ready state (has NodeName, NodeId, and Address)
func BeReady() gomegatypes.GomegaMatcher {
	return SatisfyAll(
		HaveField("Spec.NodeName", Not(BeEmpty())),
		HaveField("Status.DRBD.Config.NodeId", Not(BeNil())),
		HaveField("Status.DRBD.Config.Address", Not(BeNil())),
	)
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

// Enqueue checks that handler returns a single request.
func Enqueue(request reconcile.Request) gomegatypes.GomegaMatcher {
	return ContainElement(Equal(request))
}
