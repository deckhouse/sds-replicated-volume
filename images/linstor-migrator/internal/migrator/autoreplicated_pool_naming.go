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

package migrator

import (
	"strings"

	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

const autoRSPNameSlugMaxLen = 50

// AutoReplicatedStoragePoolName returns the deterministic Kubernetes name for a migration
// ReplicatedStoragePool derived from a LINSTOR storage pool name.
func AutoReplicatedStoragePoolName(linstorPoolName string) string {
	return config.AutoReplicatedStoragePoolNamePrefix + slugForAutoRSPName(linstorPoolName)
}

func slugForAutoRSPName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "pool"
	}
	if len(out) > autoRSPNameSlugMaxLen {
		out = out[:autoRSPNameSlugMaxLen]
		out = strings.TrimRight(out, "-")
	}
	if out == "" {
		return "pool"
	}
	return out
}
