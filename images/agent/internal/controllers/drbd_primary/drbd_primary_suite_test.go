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

package drbdprimary_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestDrbdPrimary(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DrbdPrimary Suite")
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

// HaveNoErrors returns a matcher that checks if an RVR has no DRBD errors
func HaveNoErrors() gomegatypes.GomegaMatcher {
	return SatisfyAny(
		HaveField("Status", BeNil()),
		HaveField("Status.DRBD", BeNil()),
		HaveField("Status.DRBD.Errors", BeNil()),
		SatisfyAll(
			HaveField("Status.DRBD.Errors.LastPrimaryError", BeNil()),
			HaveField("Status.DRBD.Errors.LastSecondaryError", BeNil()),
		),
	)
}

// HavePrimaryError returns a matcher that checks if an RVR has a primary error
func HavePrimaryError(output string, exitCode int) gomegatypes.GomegaMatcher {
	return SatisfyAll(
		HaveField("Status.DRBD.Errors.LastPrimaryError", Not(BeNil())),
		HaveField("Status.DRBD.Errors.LastPrimaryError.Output", Equal(output)),
		HaveField("Status.DRBD.Errors.LastPrimaryError.ExitCode", Equal(exitCode)),
		HaveField("Status.DRBD.Errors.LastSecondaryError", BeNil()),
	)
}

// HaveSecondaryError returns a matcher that checks if an RVR has a secondary error
func HaveSecondaryError(output string, exitCode int) gomegatypes.GomegaMatcher {
	return SatisfyAll(
		HaveField("Status.DRBD.Errors.LastSecondaryError", Not(BeNil())),
		HaveField("Status.DRBD.Errors.LastSecondaryError.Output", Equal(output)),
		HaveField("Status.DRBD.Errors.LastSecondaryError.ExitCode", Equal(exitCode)),
		HaveField("Status.DRBD.Errors.LastPrimaryError", BeNil()),
	)
}
