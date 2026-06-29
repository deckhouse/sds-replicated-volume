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

package controller

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testLoaderNamespace  = "d8-sds-replicated-volume"
	testLoaderSecretName = "d8-sds-replicated-volume-controller-config"
)

func newLoaderFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := apiruntime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("unable to add clientgo scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}

func mkLoaderSecret(data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testLoaderNamespace,
			Name:      testLoaderSecretName,
		},
		Data: data,
	}
}

func TestGetStorageClassLabelIgnoredPrefixes_NamespaceEmpty(t *testing.T) {
	cl := newLoaderFakeClient(t)
	got, err := getStorageClassLabelIgnoredPrefixes(context.Background(), cl, "", testLoaderSecretName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil slice for empty namespace, got %v", got)
	}
}

func TestGetStorageClassLabelIgnoredPrefixes_NameEmpty(t *testing.T) {
	cl := newLoaderFakeClient(t)
	got, err := getStorageClassLabelIgnoredPrefixes(context.Background(), cl, testLoaderNamespace, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil slice for empty name, got %v", got)
	}
}

func TestGetStorageClassLabelIgnoredPrefixes_SecretMissing(t *testing.T) {
	cl := newLoaderFakeClient(t)
	got, err := getStorageClassLabelIgnoredPrefixes(context.Background(), cl, testLoaderNamespace, testLoaderSecretName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil slice for missing Secret, got %v", got)
	}
}

func TestGetStorageClassLabelIgnoredPrefixes_NoConfigKey(t *testing.T) {
	secret := mkLoaderSecret(map[string][]byte{"unrelated": []byte("x")})
	cl := newLoaderFakeClient(t, secret)
	got, err := getStorageClassLabelIgnoredPrefixes(context.Background(), cl, testLoaderNamespace, testLoaderSecretName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil slice when config key is absent, got %v", got)
	}
}

func TestGetStorageClassLabelIgnoredPrefixes_EmptyConfigValue(t *testing.T) {
	secret := mkLoaderSecret(map[string][]byte{"config": {}})
	cl := newLoaderFakeClient(t, secret)
	got, err := getStorageClassLabelIgnoredPrefixes(context.Background(), cl, testLoaderNamespace, testLoaderSecretName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil slice for empty config value, got %v", got)
	}
}

func TestGetStorageClassLabelIgnoredPrefixes_FieldAbsent(t *testing.T) {
	secret := mkLoaderSecret(map[string][]byte{
		"config": []byte("nodeSelector:\n  kubernetes.io/os: linux\n"),
	})
	cl := newLoaderFakeClient(t, secret)
	got, err := getStorageClassLabelIgnoredPrefixes(context.Background(), cl, testLoaderNamespace, testLoaderSecretName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil slice when field is absent, got %v", got)
	}
}

func TestGetStorageClassLabelIgnoredPrefixes_ValidList(t *testing.T) {
	cfg := []byte(`nodeSelector:
  kubernetes.io/os: linux
storageClassLabelIgnoredPrefixes:
  - argocd.argoproj.io/
  - kustomize.toolkit.fluxcd.io/
  - helm.toolkit.fluxcd.io/
  - fleet.cattle.io/
  - app.kubernetes.io/managed-by
  - kubernetes.io/
`)
	secret := mkLoaderSecret(map[string][]byte{"config": cfg})
	cl := newLoaderFakeClient(t, secret)

	got, err := getStorageClassLabelIgnoredPrefixes(context.Background(), cl, testLoaderNamespace, testLoaderSecretName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"argocd.argoproj.io/",
		"kustomize.toolkit.fluxcd.io/",
		"helm.toolkit.fluxcd.io/",
		"fleet.cattle.io/",
		"app.kubernetes.io/managed-by",
		"kubernetes.io/",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected prefixes:\n  got:  %v\n  want: %v", got, want)
	}
}

func TestGetStorageClassLabelIgnoredPrefixes_MalformedYAML(t *testing.T) {
	secret := mkLoaderSecret(map[string][]byte{
		"config": []byte("this: is: not: valid: yaml\n"),
	})
	cl := newLoaderFakeClient(t, secret)

	got, err := getStorageClassLabelIgnoredPrefixes(context.Background(), cl, testLoaderNamespace, testLoaderSecretName)
	if err == nil {
		t.Fatalf("expected error on malformed YAML, got nil (prefixes: %v)", got)
	}
	if got != nil {
		t.Fatalf("expected nil slice on malformed YAML, got %v", got)
	}
}

func TestGetStorageClassLabelIgnoredPrefixes_JSONTagContract(t *testing.T) {
	cfg := []byte("storageClassLabelIgnoredPrefixes:\n  - example.com/foo\n")
	secret := mkLoaderSecret(map[string][]byte{"config": cfg})
	cl := newLoaderFakeClient(t, secret)

	got, err := getStorageClassLabelIgnoredPrefixes(context.Background(), cl, testLoaderNamespace, testLoaderSecretName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"example.com/foo"}) {
		t.Fatalf("expected [example.com/foo], got %v — `json:` tag contract may have regressed", got)
	}
}
