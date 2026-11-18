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
	"testing"

	. "github.com/onsi/gomega"
)

func TestSDNEnabled(t *testing.T) {
	g := NewWithT(t)
	t.Setenv(PodNameEnvVar, "agent")
	t.Setenv(PodNamespaceEnvVar, "module")
	t.Setenv(DRBDMinPortEnvVar, "7000")
	t.Setenv(DRBDMaxPortEnvVar, "7999")
	for _, value := range []string{"", "false", "true"} {
		t.Setenv(SDNEnabledEnvVar, value)
		cfg, err := loadConfig()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cfg.SDNEnabled()).To(Equal(value == "true"))
	}
	t.Setenv(SDNEnabledEnvVar, "invalid")
	_, err := loadConfig()
	g.Expect(err).To(HaveOccurred())
}
