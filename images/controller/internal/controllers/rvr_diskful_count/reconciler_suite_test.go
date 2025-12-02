package rvrdiskfulcount_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRvrDiskfulCount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrDiskfulCount Reconciler Suite")
}
