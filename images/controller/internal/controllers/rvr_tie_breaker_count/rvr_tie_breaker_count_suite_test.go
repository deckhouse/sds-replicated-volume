package rvrtiebreakercount_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func TestRvrTieBreakerCount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrTieBreakerCount Suite")
}

func HaveTieBreakerCount(matcher types.GomegaMatcher) types.GomegaMatcher {
	return WithTransform(func(list []v1alpha3.ReplicatedVolumeReplica) int {
		tbCount := 0
		for _, rvr := range list {
			// if rvr.Spec.ReplicatedVolumeName != "rv1" {
			// 	continue
			// }
			if rvr.Spec.Type == "TieBreaker" {
				tbCount++
			}
		}
		return tbCount
	}, matcher)
}
