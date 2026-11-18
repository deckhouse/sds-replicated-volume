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

package env

import (
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	commonenv "github.com/deckhouse/sds-replicated-volume/lib/go/common/env"
)

func TestParseStorageClassLabelIgnoredPrefixes(t *testing.T) {
	for _, raw := range []string{"", "  ", "null"} {
		got, err := parseStorageClassLabelIgnoredPrefixes(raw)
		if err != nil || got != nil {
			t.Fatalf("%q: got %v, %v; want nil, nil", raw, got, err)
		}
	}

	got, err := parseStorageClassLabelIgnoredPrefixes(`["argocd.argoproj.io/","fleet.cattle.io/"]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "argocd.argoproj.io/" || got[1] != "fleet.cattle.io/" {
		t.Fatalf("unexpected prefixes: %v", got)
	}

	if _, err := parseStorageClassLabelIgnoredPrefixes(`{"a":"b"}`); !errors.Is(err, commonenv.ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
}
func TestConfig(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Controller environment") }

var _ = Describe("SDN_ENABLED", func() {
	It("defaults off, enables explicitly, and rejects malformed values", func() {
		GinkgoT().Setenv(PodNamespaceEnvVar, "test")
		GinkgoT().Setenv(SchedulerExtenderURLEnvVar, "http://scheduler")
		GinkgoT().Setenv(SDNEnabledEnvVar, "")
		cfg, err := loadConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.SDNEnabled()).To(BeFalse())
		GinkgoT().Setenv(SDNEnabledEnvVar, "true")
		cfg, err = loadConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.SDNEnabled()).To(BeTrue())
		GinkgoT().Setenv(SDNEnabledEnvVar, "bad")
		_, err = loadConfig()
		Expect(err).To(MatchError(ContainSubstring(SDNEnabledEnvVar)))
	})
})
