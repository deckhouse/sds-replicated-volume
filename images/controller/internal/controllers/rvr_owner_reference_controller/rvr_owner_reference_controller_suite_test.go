package rvrownerreferencecontroller_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRvrOwnerReferenceController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrOwnerReferenceController Suite")
}
