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

// Package load reads gzipped multi-document LINSTOR CR YAML backups (crs.gz).
package load

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// FromFile opens path (plain YAML or .gz) and returns all documents.
func FromFile(path string) ([]*unstructured.Unstructured, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open backup %q: %w", path, err)
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gzr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("gzip open %q: %w", path, err)
		}
		defer gzr.Close()
		r = gzr
	}

	return decodeMultiDoc(r)
}

func decodeMultiDoc(r io.Reader) ([]*unstructured.Unstructured, error) {
	yr := yaml.NewYAMLReader(bufio.NewReader(r))
	var out []*unstructured.Unstructured

	for {
		doc, err := yr.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("read YAML document: %w", err)
		}
		if len(strings.TrimSpace(string(doc))) == 0 {
			continue
		}

		var u unstructured.Unstructured
		if err := yaml.Unmarshal(doc, &u); err != nil {
			return nil, fmt.Errorf("parse YAML document: %w", err)
		}
		if u.GetKind() == "" {
			continue
		}
		out = append(out, &u)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no Kubernetes resources found in backup")
	}
	return out, nil
}
