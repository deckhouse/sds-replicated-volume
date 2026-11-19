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

package templatetests

import (
	"os"
	"slices"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"helm.sh/helm/v3/pkg/releaseutil"
	"sigs.k8s.io/yaml"
)

const (
	namespace    = "d8-sds-replicated-volume"
	sensitiveKey = "x-kubernetes-sensitive-data"
	group        = "storage.deckhouse.io"

	replicatedVolumesCRD = "../crds/newcontrolplane/storage.deckhouse.io_replicatedvolumes.yaml"
	drbdResourcesCRD     = "../crds/newcontrolplane/storage.deckhouse.io_drbdresources.yaml"

	controllerRole      = "d8:sds-replicated-volume:controller"
	agentRole           = "d8:sds-replicated-volume:agent"
	adminKubeconfigRole = "d8:sds-replicated-volume:admin-kubeconfig"
)

var (
	readVerbs  = []string{"get", "list", "watch"}
	writeVerbs = []string{"create", "update", "patch"}

	deckhouseVersions = []string{"1.76.5", "1.78.0"}

	controlPlaneMigrations = []string{"not_started", "stage1_completed", "all_completed"}

	rbacTemplates = []string{
		"templates/rbac-for-us.yaml",
		"templates/user-authz-cluster-roles.yaml",
		"templates/rbacv2/",
		"templates/controller/rbac-for-us.yaml",
		"templates/agent/rbac-for-us.yaml",
		"templates/csi/rbac-for-us.yaml",
		"templates/linstor-migrator/rbac-for-us.yaml",
		"templates/webhooks/rbac-for-us.yaml",
	}

	sensitiveResources = map[string]string{
		"replicatedvolumes": "replicatedvolumes/sensitive",
		"drbdresources":     "drbdresources/sensitive",
	}
)

type object = map[string]any

func TestSharedSecretsAreSensitive(t *testing.T) {
	assertSensitive(t, replicatedVolumesCRD, map[string]bool{
		"status.datamesh.sharedSecret":    true,
		"status.datamesh.sharedSecretAlg": false,
		"status.datamesh.members":         false,
	})
	assertSensitive(t, drbdResourcesCRD, map[string]bool{
		"spec.peers[].sharedSecret":    true,
		"spec.peers[].sharedSecretAlg": false,
		"spec.peers[].name":            false,
		"spec.peers[].paths":           false,
	})
}

func TestValueConsumersReadSensitiveFields(t *testing.T) {
	for _, tc := range renders() {
		t.Run(tc.name, func(t *testing.T) {
			objects := render(t, tc.version, tc.migration)
			controller := find(t, objects, controllerRole)
			assertVerbs(t, controller, sensitiveResources["replicatedvolumes"], readVerbs)
			assertVerbs(t, controller, sensitiveResources["drbdresources"], readVerbs)
			assertVerbs(t, find(t, objects, agentRole), sensitiveResources["drbdresources"], readVerbs)
		})
	}
}

func TestValueConsumersGetUnmaskedWriteResponses(t *testing.T) {
	for _, tc := range renders() {
		t.Run(tc.name, func(t *testing.T) {
			objects := render(t, tc.version, tc.migration)
			for _, roleName := range []string{controllerRole, agentRole} {
				role := find(t, objects, roleName)
				for resource, sensitive := range sensitiveResources {
					var want []string
					for _, verb := range writeVerbs {
						if slices.Contains(granted(role, resource), verb) || slices.Contains(granted(role, resource+"/status"), verb) {
							want = append(want, verb)
						}
					}
					assertVerbs(t, role, sensitive, want)
				}
			}
		})
	}
}

func TestAdminKubeconfigReadsSensitiveFields(t *testing.T) {
	for _, tc := range renders() {
		t.Run(tc.name, func(t *testing.T) {
			role := find(t, render(t, tc.version, tc.migration), adminKubeconfigRole)
			for _, sensitive := range sensitiveResources {
				assertVerbs(t, role, sensitive, readVerbs)
			}
		})
	}
}

func TestOnlyValueConsumersGetSensitiveSubresources(t *testing.T) {
	allowed := []string{controllerRole, agentRole, adminKubeconfigRole}
	for _, tc := range renders() {
		t.Run(tc.name, func(t *testing.T) {
			for _, obj := range render(t, tc.version, tc.migration) {
				if obj["kind"] != "ClusterRole" && obj["kind"] != "Role" {
					continue
				}
				for _, sensitive := range sensitiveResources {
					verbs := granted(obj, sensitive)
					if len(verbs) == 0 {
						continue
					}
					if isUserRole(obj) || !slices.Contains(allowed, name(obj)) {
						t.Errorf("%s %s grants %v on %s", obj["kind"], name(obj), verbs, sensitive)
					}
				}
			}
		})
	}
}

type renderCase struct {
	name      string
	version   string
	migration string
}

func renders() []renderCase {
	var cases []renderCase
	for _, version := range deckhouseVersions {
		for _, migration := range controlPlaneMigrations {
			cases = append(cases, renderCase{name: version + "/" + migration, version: version, migration: migration})
		}
	}
	return cases
}

func isUserRole(obj object) bool {
	if _, ok := field(obj, "metadata", "annotations", "user-authz.deckhouse.io/access-level").(string); ok {
		return true
	}
	labels, _ := field(obj, "metadata", "labels").(object)
	for key := range labels {
		if strings.HasPrefix(key, "rbac.deckhouse.io/aggregate-to-") {
			return true
		}
	}
	return false
}

func assertSensitive(t *testing.T, path string, fields map[string]bool) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var crd object
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	versions, _ := field(crd, "spec", "versions").([]any)
	if len(versions) == 0 {
		t.Fatalf("%s has no versions", path)
	}
	for _, v := range versions {
		version, _ := v.(object)
		root, _ := field(version, "schema", "openAPIV3Schema").(object)
		for fieldPath, sensitive := range fields {
			property, ok := schemaAt(root, fieldPath)
			if !ok {
				t.Errorf("%s %v: %s is missing", path, version["name"], fieldPath)
				continue
			}
			if got := property[sensitiveKey] == true; got != sensitive {
				t.Errorf("%s %v: %s has %s=%v, want %v", path, version["name"], fieldPath, sensitiveKey, property[sensitiveKey], sensitive)
			}
		}
	}
}

func schemaAt(root object, fieldPath string) (object, bool) {
	current := root
	for _, part := range strings.Split(fieldPath, ".") {
		items := strings.HasSuffix(part, "[]")
		next, ok := field(current, "properties", strings.TrimSuffix(part, "[]")).(object)
		if !ok {
			return nil, false
		}
		if items {
			if next, ok = next["items"].(object); !ok {
				return nil, false
			}
		}
		current = next
	}
	return current, true
}

func render(t *testing.T, deckhouseVersion, migration string) []object {
	t.Helper()

	c, err := loader.LoadDir("..")
	if err != nil {
		t.Fatalf("load the chart: %v", err)
	}
	c.Templates = slices.DeleteFunc(c.Templates, func(f *chart.File) bool {
		if strings.HasPrefix(f.Name, "templates/_") {
			return false
		}
		for _, keep := range rbacTemplates {
			if f.Name == keep || strings.HasSuffix(keep, "/") && strings.HasPrefix(f.Name, keep) {
				return false
			}
		}
		return true
	})

	values := object{
		"global": object{
			"deckhouseVersion": deckhouseVersion,
			"enabledModules":   []any{"prometheus", "sdn-crd"},
			"discovery":        object{"apiVersions": []any{}},
			"modulesImages": object{
				"registry": object{"base": "registry.example.com/deckhouse", "dockercfg": "e30="},
			},
		},
		"sdsReplicatedVolume": object{
			"registry": object{"base": "registry.example.com/modules/sds-replicated-volume", "dockercfg": "e30="},
			"internal": object{
				"newControlPlane":       true,
				"controlPlaneMigration": migration,
			},
		},
	}

	renderValues, err := chartutil.ToRenderValues(c, values, chartutil.ReleaseOptions{
		Name:      c.Name(),
		Namespace: namespace,
		IsInstall: true,
	}, chartutil.DefaultCapabilities)
	if err != nil {
		t.Fatalf("build the render values: %v", err)
	}

	files, err := engine.Render(c, renderValues)
	if err != nil {
		t.Fatalf("render the chart: %v", err)
	}

	var objects []object
	for file, content := range files {
		if !strings.HasSuffix(file, ".yaml") {
			continue
		}
		for _, manifest := range releaseutil.SplitManifests(content) {
			var obj object
			if err := yaml.Unmarshal([]byte(manifest), &obj); err != nil {
				t.Fatalf("parse a document of %s: %v\n%s", file, err, manifest)
			}
			if obj != nil {
				objects = append(objects, obj)
			}
		}
	}
	return objects
}

func granted(role object, resource string) []string {
	var verbs []string
	rules, _ := role["rules"].([]any)
	for _, r := range rules {
		rule, _ := r.(object)
		groups, _ := rule["apiGroups"].([]any)
		resources, _ := rule["resources"].([]any)
		if !slices.Contains(groups, any(group)) || !slices.Contains(resources, any(resource)) {
			continue
		}
		if names, _ := rule["resourceNames"].([]any); len(names) > 0 {
			continue
		}
		ruleVerbs, _ := rule["verbs"].([]any)
		for _, v := range ruleVerbs {
			if s, ok := v.(string); ok && !slices.Contains(verbs, s) {
				verbs = append(verbs, s)
			}
		}
	}
	slices.Sort(verbs)
	return verbs
}

func assertVerbs(t *testing.T, role object, resource string, want []string) {
	t.Helper()

	got := granted(role, resource)
	for _, verb := range want {
		if !slices.Contains(got, verb) {
			t.Errorf("%s: %s grants %v, missing %s", name(role), resource, got, verb)
		}
	}
}

func find(t *testing.T, objects []object, objectName string) object {
	t.Helper()

	for _, obj := range objects {
		if obj["kind"] == "ClusterRole" && name(obj) == objectName {
			return obj
		}
	}
	t.Fatalf("ClusterRole %s is not rendered", objectName)
	return nil
}

func name(obj object) string {
	s, _ := field(obj, "metadata", "name").(string)
	return s
}

func field(obj object, path ...string) any {
	var current any = obj
	for _, key := range path {
		m, ok := current.(object)
		if !ok {
			return nil
		}
		current = m[key]
	}
	return current
}
