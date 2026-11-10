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

	commonenv "github.com/deckhouse/sds-replicated-volume/lib/go/common/env"
)

func TestLoadConfig(t *testing.T) {
	for _, tc := range []struct{ name, missing string }{
		{name: "pod identity"},
		{name: "missing pod name", missing: PodNameEnvVar},
		{name: "missing pod namespace", missing: PodNamespaceEnvVar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(NodeNameEnvVar, "node-1")
			t.Setenv(PodNameEnvVar, "agent-abc")
			t.Setenv(PodNamespaceEnvVar, "d8-sds-replicated-volume")
			t.Setenv(DRBDMinPortEnvVar, "7000")
			t.Setenv(DRBDMaxPortEnvVar, "7999")
			if tc.missing != "" {
				t.Setenv(tc.missing, "")
			}
			cfg, err := loadConfig()
			if tc.missing != "" {
				if !errors.Is(err, commonenv.ErrInvalidConfig) {
					t.Fatalf("expected invalid config for %s, got %v", tc.missing, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.PodName() != "agent-abc" || cfg.PodNamespace() != "d8-sds-replicated-volume" || cfg.NodeName() != "node-1" {
				t.Fatalf("unexpected identity: %s/%s on %s", cfg.PodNamespace(), cfg.PodName(), cfg.NodeName())
			}
			if cfg.DRBDMinPort() != 7000 || cfg.DRBDMaxPort() != 7999 {
				t.Fatal("unexpected port range")
			}
		})
	}
}
