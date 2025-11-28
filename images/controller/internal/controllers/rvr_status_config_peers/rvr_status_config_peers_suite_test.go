package rvr_status_config_peers_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRvrStatusConfigPeers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrStatusConfigPeers Suite")
}
