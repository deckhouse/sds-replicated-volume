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
	"maps"
	"slices"
	"testing"

	. "github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
)

// TestAllCertGroups also covers the cert group configurations being valid: an invalid one panics
// in MustNewGenSelfSignedTLSGroupHookConf, which would take the whole hook binary down.
func TestAllCertGroups(t *testing.T) {
	caSecrets := map[string]struct{}{}
	leafSecrets := map[string]struct{}{}

	for group := range AllCertGroups() {
		if len(group) == 0 {
			t.Fatal("empty cert group")
		}

		caConf := group[0]

		if _, dup := caSecrets[caConf.CASecretName]; dup {
			t.Fatalf("ca secret %s is shared by several groups", caConf.CASecretName)
		}
		caSecrets[caConf.CASecretName] = struct{}{}

		if caConf.CAExpiryDuration != DefaultCAExpiredDuration {
			t.Fatalf(
				"ca %s expires in %s, expected %s",
				caConf.CASecretName, caConf.CAExpiryDuration, DefaultCAExpiredDuration,
			)
		}

		for _, conf := range group {
			if conf.Namespace != ModuleNamespace {
				t.Fatalf("cert %s lives in namespace %s", conf.TLSSecretName, conf.Namespace)
			}

			if conf.CertExpiryDuration >= conf.CAExpiryDuration {
				t.Fatalf(
					"cert %s expires in %s, which is not shorter than the %s of its ca",
					conf.TLSSecretName, conf.CertExpiryDuration, conf.CAExpiryDuration,
				)
			}

			if conf.CertOutdatedDuration >= conf.CertExpiryDuration {
				t.Fatalf(
					"cert %s is renewed %s before expiration, which is not shorter than its %s lifespan",
					conf.TLSSecretName, conf.CertOutdatedDuration, conf.CertExpiryDuration,
				)
			}

			if _, dup := leafSecrets[conf.TLSSecretName]; dup {
				t.Fatalf("secret %s is used by several certs", conf.TLSSecretName)
			}
			leafSecrets[conf.TLSSecretName] = struct{}{}
		}
	}

	if len(caSecrets) == 0 {
		t.Fatal("no cert groups")
	}

	for name := range caSecrets {
		if _, clash := leafSecrets[name]; clash {
			t.Fatalf("secret %s is used both for a ca and for a certificate", name)
		}
	}
}

func TestAllCertSecretNames(t *testing.T) {
	names := AllCertSecretNames()

	for _, expected := range []string{
		"linstor-ca",
		"linstor-controller-https-cert",
		"linstor-client-https-cert",
		"linstor-controller-ssl-cert",
		"linstor-node-ssl-cert",
		"webhooks-ca",
		"webhooks-https-certs",
		"spaas-ca",
		"spaas-certs",
		"linstor-scheduler-extender-ca",
		"linstor-scheduler-extender-https-certs",
	} {
		if _, ok := names[expected]; !ok {
			t.Errorf("secret %s is not reported as belonging to the module", expected)
		}
	}

	for _, obsolete := range ObsoleteSecretNames {
		if _, ok := names[obsolete]; ok {
			t.Errorf("obsolete secret %s is still a part of a cert group", obsolete)
		}
	}

	if len(names) != 11 {
		t.Errorf("unexpected cert secrets: %v", slices.Sorted(maps.Keys(names)))
	}
}
