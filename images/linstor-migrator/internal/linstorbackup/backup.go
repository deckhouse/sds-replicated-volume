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

// Package linstorbackup dumps LINSTOR API CustomResourceDefinitions and their custom resources
// (internal.linstor.linbit.com) to gzipped multi-document YAML.
package linstorbackup

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

const (
	// APIGroup is the LINSTOR Kubernetes API group backed up from CRD discovery (spec.group).
	APIGroup = "internal.linstor.linbit.com"

	backupCRDsGzFile = "crds.gz"
	backupCRsGzFile  = "crs.gz"
	backupReadme     = "readme.txt"
)

func backupReadmeText() string {
	exampleDir := filepath.Join(config.MigratorHostDir, config.MigratorLinstorBackupDirName)
	exampleCRDs := filepath.Join(exampleDir, backupCRDsGzFile)
	exampleCRs := filepath.Join(exampleDir, backupCRsGzFile)
	return fmt.Sprintf(`LINSTOR backup (API group internal.linstor.linbit.com)
========================================================

Files:
  crds.gz — gzip multi-document YAML: one CustomResourceDefinition per document
            (spec.group internal.linstor.linbit.com).
  crs.gz  — gzip multi-document YAML: every custom resource instance for those CRDs.

Restore:
  1. mkdir -p /tmp/linstor-restore && cd /tmp/linstor-restore
  2. gunzip -c %s > crds.yaml
  3. kubectl apply -f crds.yaml
  4. gunzip -c %s > crs.yaml
  5. kubectl apply -f crs.yaml

Notes:
  - Apply CRDs before CRs. Status and server-owned metadata (managedFields, resourceVersion, uid,
    creationTimestamp, generation) are omitted from both files so manifests are apply-friendly.
`, exampleCRDs, exampleCRs)
}

// BackupDir returns the host path for LINSTOR CR backups under MigratorHostDir.
func BackupDir() string {
	return filepath.Join(config.MigratorHostDir, config.MigratorLinstorBackupDirName)
}

// WriteGroupBackup lists all CustomResourceDefinitions for APIGroup, lists every instance of each
// resource, and writes crds.gz (CRD definitions), crs.gz (custom resources), and readme.txt into dir.
func WriteGroupBackup(ctx context.Context, kClient client.Client, dyn dynamic.Interface, log *slog.Logger, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backup directory %q: %w", dir, err)
	}

	crds := &apiextensionsv1.CustomResourceDefinitionList{}
	if err := kClient.List(ctx, crds); err != nil {
		return fmt.Errorf("list CustomResourceDefinitions: %w", err)
	}

	var linstorCRDs []apiextensionsv1.CustomResourceDefinition
	for i := range crds.Items {
		crd := crds.Items[i]
		if crd.Spec.Group != APIGroup {
			continue
		}
		linstorCRDs = append(linstorCRDs, crd)
	}
	sort.Slice(linstorCRDs, func(i, j int) bool { return linstorCRDs[i].Name < linstorCRDs[j].Name })

	// Serialize each CustomResourceDefinition (apiextensions) into the multi-doc YAML for crds.gz.
	var crdYamlBuf bytes.Buffer
	for i := range linstorCRDs {
		crd := &linstorCRDs[i]
		b, err := crdToApplyFriendlyYAML(crd)
		if err != nil {
			return fmt.Errorf("marshal CRD %q: %w", crd.Name, err)
		}
		if i > 0 {
			crdYamlBuf.WriteString("---\n")
		}
		crdYamlBuf.Write(b)
		log.Debug("backed up LINSTOR CRD definition", "crd", crd.Name)
	}
	crdGzPath := filepath.Join(dir, backupCRDsGzFile)
	if err := writeGzipFile(crdGzPath, crdYamlBuf.Bytes()); err != nil {
		return err
	}

	// List and serialize every custom resource instance per LINSTOR CRD into the multi-doc YAML for crs.gz.
	var crsYamlBuf bytes.Buffer
	crDocCount := 0
	for _, crd := range linstorCRDs {
		ver, err := storageVersionName(&crd)
		if err != nil {
			return fmt.Errorf("CRD %q: %w", crd.Name, err)
		}
		gvr := schema.GroupVersionResource{
			Group:    crd.Spec.Group,
			Version:  ver,
			Resource: crd.Spec.Names.Plural,
		}
		res := dyn.Resource(gvr)
		var list *unstructured.UnstructuredList
		if crd.Spec.Scope == apiextensionsv1.ClusterScoped {
			list, err = res.List(ctx, metav1.ListOptions{})
		} else {
			list, err = res.Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
		}
		if err != nil {
			if apierrors.IsForbidden(err) {
				return fmt.Errorf("list %s/%s: %w", gvr.GroupVersion().String(), gvr.Resource, err)
			}
			return fmt.Errorf("list %s/%s: %w", gvr.GroupVersion().String(), gvr.Resource, err)
		}
		for _, item := range list.Items {
			clean := item.DeepCopy()
			stripApplyUnfriendlyFields(clean.Object)
			b, err := yaml.Marshal(clean.Object)
			if err != nil {
				return fmt.Errorf("marshal %s %s/%s: %w", crd.Name, clean.GetNamespace(), clean.GetName(), err)
			}
			if crDocCount > 0 {
				crsYamlBuf.WriteString("---\n")
			}
			crsYamlBuf.Write(b)
			crDocCount++
		}
		log.Debug("backed up LINSTOR custom resources", "crd", crd.Name, "count", len(list.Items))
	}
	crsGzPath := filepath.Join(dir, backupCRsGzFile)
	if err := writeGzipFile(crsGzPath, crsYamlBuf.Bytes()); err != nil {
		return err
	}

	readmePath := filepath.Join(dir, backupReadme)
	if err := os.WriteFile(readmePath, []byte(backupReadmeText()), 0o644); err != nil {
		return fmt.Errorf("write readme: %w", err)
	}

	log.Info("wrote LINSTOR backup",
		"dir", dir,
		"crdFile", backupCRDsGzFile, "crdDocuments", len(linstorCRDs),
		"crFile", backupCRsGzFile, "crDocuments", crDocCount,
	)
	return nil
}

func crdToApplyFriendlyYAML(crd *apiextensionsv1.CustomResourceDefinition) ([]byte, error) {
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(crd.DeepCopy())
	if err != nil {
		return nil, err
	}
	stripApplyUnfriendlyFields(u)
	// controller-runtime typed List/Get often leaves embedded TypeMeta empty, so ToUnstructured
	// omits apiVersion/kind; kubectl apply requires them.
	u["apiVersion"] = apiextensionsv1.SchemeGroupVersion.String()
	u["kind"] = "CustomResourceDefinition"
	return yaml.Marshal(u)
}

// stripApplyUnfriendlyFields removes status and server-populated metadata so kubectl apply restore works.
func stripApplyUnfriendlyFields(obj map[string]interface{}) {
	unstructured.RemoveNestedField(obj, "metadata", "managedFields")
	unstructured.RemoveNestedField(obj, "metadata", "resourceVersion")
	unstructured.RemoveNestedField(obj, "metadata", "uid")
	unstructured.RemoveNestedField(obj, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(obj, "metadata", "generation")
	unstructured.RemoveNestedField(obj, "status")
}

func writeGzipFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	gzw := gzip.NewWriter(f)
	if _, err := gzw.Write(data); err != nil {
		_ = gzw.Close()
		_ = f.Close()
		return fmt.Errorf("gzip write %q: %w", path, err)
	}
	if err := gzw.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("gzip close %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %q: %w", path, err)
	}
	return nil
}

func storageVersionName(crd *apiextensionsv1.CustomResourceDefinition) (string, error) {
	for _, v := range crd.Spec.Versions {
		if v.Storage {
			return v.Name, nil
		}
	}
	if len(crd.Spec.Versions) > 0 {
		return crd.Spec.Versions[0].Name, nil
	}
	return "", fmt.Errorf("no versions")
}
