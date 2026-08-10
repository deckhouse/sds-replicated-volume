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

package certs

import (
	"iter"

	tlscertificate "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
)

// ObsoleteSecretNames are secrets which used to keep certificates of components removed from the
// module. Nothing refreshes them anymore, so they are deleted to keep them out of the
// expiring-certificates alert.
var ObsoleteSecretNames = []string{
	// the linstor-scheduler-admission component was removed from the module, its certificate was
	// a part of the webhook cert group until the CA of that group became a standalone secret
	"linstor-scheduler-admission-certs",
}

// AllCertGroups returns every cert group managed by the module.
func AllCertGroups() iter.Seq[tlscertificate.GenSelfSignedTLSGroupHookConf] {
	return func(yield func(tlscertificate.GenSelfSignedTLSGroupHookConf) bool) {
		if !yield(LinstorCertConfigs()) {
			return
		}
		if !yield(WebhookCertConfigs()) {
			return
		}
		if !yield(SchedulerExtenderCertConfig) {
			return
		}
		if !yield(SpaasCertConfig) {
			return
		}
	}
}

// AllCertSecretNames returns the names of every secret the module keeps certificates in, both
// the leaf certificates and the CAs signing them.
func AllCertSecretNames() map[string]struct{} {
	res := map[string]struct{}{}

	for group := range AllCertGroups() {
		res[group[0].CASecretName] = struct{}{}

		for _, conf := range group {
			res[conf.TLSSecretName] = struct{}{}
		}
	}

	return res
}
