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

package tests

import (
	"log/slog"
	"os"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrlLog "sigs.k8s.io/controller-runtime/pkg/log"

	// Must be first: applies storage-e2e env defaults before storage-e2e packages load.
	_ "github.com/deckhouse/sds-replicated-volume/e2e/linstor-migrator/bootstrapenv"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

// testClusterResources holds SSH/kube state for CleanupTestClusterResources in AfterSuite.
var testClusterResources *cluster.TestClusterResources

func TestLinstorMigrator(t *testing.T) {
	RegisterFailHandler(Fail)

	level := slog.LevelInfo
	if os.Getenv("TEST_DEBUG") == "1" {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02T15:04:05"))
			}
			return a
		},
	}
	logger := slog.New(slog.NewTextHandler(GinkgoWriter, opts))
	slog.SetDefault(logger)
	ctrlLog.SetLogger(logr.FromSlogHandler(logger.Handler()))

	suiteConfig, reporterConfig := GinkgoConfiguration()
	reporterConfig.Verbose = true
	reporterConfig.ShowNodeEvents = false

	RunSpecs(t, "Linstor Migrator E2E Suite", suiteConfig, reporterConfig)
}

var _ = BeforeSuite(func() {
	err := validateMigratorEnv()
	Expect(err).NotTo(HaveOccurred(), "storage-e2e environment validation failed")

	// Create a nested test cluster or connect to an existing one (see TEST_CLUSTER_CREATE_MODE).
	testClusterResources = cluster.CreateOrConnectToTestCluster()
})

var _ = AfterSuite(func() {
	cluster.CleanupTestClusterResources(testClusterResources)
})
