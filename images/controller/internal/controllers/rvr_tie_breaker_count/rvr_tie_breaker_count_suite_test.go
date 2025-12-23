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

package rvrtiebreakercount_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestRvrTieBreakerCount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrTieBreakerCount Suite")
}

func HaveTieBreakerCount(matcher types.GomegaMatcher) types.GomegaMatcher {
	return WithTransform(func(list []v1alpha1.ReplicatedVolumeReplica) int {
		tbCount := 0
		for _, rvr := range list {
			if rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
				tbCount++
			}
		}
		return tbCount
	}, matcher)
}
