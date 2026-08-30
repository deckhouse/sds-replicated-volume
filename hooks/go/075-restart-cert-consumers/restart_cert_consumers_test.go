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

package restartcertconsumers

import (
	"slices"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
)

func testCertSecretNames() map[string]struct{} {
	res := map[string]struct{}{}
	for name := range testCertSecrets() {
		res[name] = struct{}{}
	}

	return res
}

func testCertSecrets() map[string]*v1.Secret {
	return map[string]*v1.Secret{
		"linstor-client-https-cert": {
			Data: map[string][]byte{"ca.crt": []byte("client-ca"), "tls.crt": []byte("client-crt")},
		},
		"linstor-node-ssl-cert": {
			Data: map[string][]byte{"ca.crt": []byte("node-ca"), "tls.crt": []byte("node-crt")},
		},
	}
}

func TestConsumedCertSecrets(t *testing.T) {
	spec := &v1.PodSpec{
		Volumes: []v1.Volume{
			{VolumeSource: v1.VolumeSource{Secret: &v1.SecretVolumeSource{SecretName: "linstor-node-ssl-cert"}}},
			{VolumeSource: v1.VolumeSource{Secret: &v1.SecretVolumeSource{SecretName: "deckhouse-registry"}}},
			{VolumeSource: v1.VolumeSource{Projected: &v1.ProjectedVolumeSource{
				Sources: []v1.VolumeProjection{
					{Secret: &v1.SecretProjection{
						LocalObjectReference: v1.LocalObjectReference{Name: "linstor-node-ssl-cert"},
					}},
				},
			}}},
		},
		InitContainers: []v1.Container{
			{EnvFrom: []v1.EnvFromSource{
				{SecretRef: &v1.SecretEnvSource{
					LocalObjectReference: v1.LocalObjectReference{Name: "linstor-client-https-cert"},
				}},
			}},
		},
		Containers: []v1.Container{
			{Env: []v1.EnvVar{
				{Name: "LS_USER_CERTIFICATE", ValueFrom: &v1.EnvVarSource{SecretKeyRef: &v1.SecretKeySelector{
					LocalObjectReference: v1.LocalObjectReference{Name: "linstor-client-https-cert"},
					Key:                  "tls.crt",
				}}},
				{Name: "UNRELATED", Value: "x"},
			}},
		},
	}

	got := consumedCertSecrets(testCertSecretNames(), spec)
	want := []string{"linstor-client-https-cert", "linstor-node-ssl-cert"}

	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestConsumedCertSecretsIgnoresForeignWorkloads(t *testing.T) {
	spec := &v1.PodSpec{
		Volumes: []v1.Volume{
			{VolumeSource: v1.VolumeSource{Secret: &v1.SecretVolumeSource{SecretName: "wildcard-example-com"}}},
		},
	}

	if got := consumedCertSecrets(testCertSecretNames(), spec); len(got) != 0 {
		t.Fatalf("expected no cert secrets, got %v", got)
	}
}

func TestCertChecksumChangesWithCertificate(t *testing.T) {
	certSecrets := testCertSecrets()
	consumed := []string{"linstor-client-https-cert"}

	before := certChecksum(certSecrets, consumed)

	certSecrets["linstor-client-https-cert"].Data["tls.crt"] = []byte("renewed-crt")
	if after := certChecksum(certSecrets, consumed); after == before {
		t.Fatal("checksum did not change after a renewal")
	}
}

func TestCertChecksumIgnoresPrivateKey(t *testing.T) {
	certSecrets := testCertSecrets()
	consumed := []string{"linstor-client-https-cert"}

	before := certChecksum(certSecrets, consumed)

	certSecrets["linstor-client-https-cert"].Data["tls.key"] = []byte("some-key")
	if after := certChecksum(certSecrets, consumed); after != before {
		t.Fatal("checksum changed after touching the private key only")
	}
}

func TestCertChecksumPatch(t *testing.T) {
	t.Run("first reconciliation does not touch the pod template", func(t *testing.T) {
		patch, err := certChecksumPatch("abc", false)
		if err != nil {
			t.Fatal(err)
		}

		if strings.Contains(string(patch), "template") {
			t.Fatalf("unexpected pod template patch: %s", patch)
		}
	})

	t.Run("renewal patches the pod template", func(t *testing.T) {
		patch, err := certChecksumPatch("abc", true)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(string(patch), "template") {
			t.Fatalf("pod template is not patched: %s", patch)
		}
	})
}
