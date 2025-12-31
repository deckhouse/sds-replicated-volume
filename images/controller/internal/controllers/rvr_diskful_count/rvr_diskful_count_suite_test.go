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

package rvrdiskfulcount_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestRvrDiskfulCount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrDiskfulCount Suite")
}

// HaveCondition is a matcher that checks if a slice of conditions contains a condition
// with the specified type that matches the provided matcher.
func HaveCondition(conditionType string, matcher OmegaMatcher) OmegaMatcher {
	return ContainElement(SatisfyAll(
		HaveField("Type", Equal(conditionType)),
		matcher,
	))
}

func Requeue() OmegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func RequestFor(o client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(o)}
}
