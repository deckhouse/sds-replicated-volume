//go:build ignore

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

// validate-crds validates generated CRD YAMLs against the Kubernetes API server
// validation logic, including CEL expression cost budget checks.
//
// Run from the api/ module directory:
//
//	go run ../hack/validate-crds.go ../crds
//
// This catches issues like:
//   - CEL expressions exceeding the per-expression cost limit
//   - Total CRD CEL cost exceeding the aggregate limit
//   - Missing MaxItems on lists with O(n²) CEL uniqueness rules
//   - Schema validation errors
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <crds-directory>\n", os.Args[0])
		os.Exit(2)
	}
	crdDir := os.Args[1]

	scheme := runtime.NewScheme()
	install.Install(scheme)
	codecFactory := serializer.NewCodecFactory(scheme)
	decoder := codecFactory.UniversalDeserializer()

	entries, err := os.ReadDir(crdDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading CRD directory %s: %v\n", crdDir, err)
		os.Exit(1)
	}

	found := 0
	failed := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "storage.deckhouse.io_") {
			continue
		}
		found++

		data, err := os.ReadFile(filepath.Join(crdDir, entry.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "  FAIL %s: reading file: %v\n", entry.Name(), err)
			failed++
			continue
		}

		obj, _, err := decoder.Decode(data, nil, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  FAIL %s: decoding YAML: %v\n", entry.Name(), err)
			failed++
			continue
		}

		v1CRD, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
		if !ok {
			fmt.Fprintf(os.Stderr, "  FAIL %s: expected *v1.CustomResourceDefinition, got %T\n", entry.Name(), obj)
			failed++
			continue
		}

		crd := &apiextensions.CustomResourceDefinition{}
		if err := scheme.Convert(v1CRD, crd, nil); err != nil {
			fmt.Fprintf(os.Stderr, "  FAIL %s: converting v1 → internal: %v\n", entry.Name(), err)
			failed++
			continue
		}

		errs := apiextensionsvalidation.ValidateCustomResourceDefinition(context.Background(), crd)
		crdFailed := false
		for _, e := range errs {
			// storedVersions is set by the API server at runtime, not in generated YAML.
			if strings.Contains(e.Field, "storedVersions") {
				continue
			}
			if !crdFailed {
				fmt.Fprintf(os.Stderr, "  FAIL %s:\n", entry.Name())
				crdFailed = true
			}
			fmt.Fprintf(os.Stderr, "    %s\n", e)
		}
		if crdFailed {
			failed++
		} else {
			fmt.Fprintf(os.Stdout, "  ok   %s\n", entry.Name())
		}
	}

	if found == 0 {
		fmt.Fprintf(os.Stderr, "no CRD files found in %s\n", crdDir)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "\n%d CRDs validated", found)
	if failed > 0 {
		fmt.Fprintf(os.Stdout, ", %d failed\n", failed)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, ", all passed\n")
}
