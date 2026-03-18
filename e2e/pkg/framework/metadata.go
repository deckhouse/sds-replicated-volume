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

package framework

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Kubernetes labels set on every e2e-created object.
const (
	LabelE2ERunKey    = "e2e.deckhouse.io/run"
	LabelE2EWorkerKey = "e2e.deckhouse.io/worker"
)

// Kubernetes annotations set on every e2e-created object.
const (
	AnnotE2ESourceKey = "e2e.deckhouse.io/source"
	AnnotE2ESpecKey   = "e2e.deckhouse.io/spec"
)

// stampRunMetadata sets the run-level label and source/spec annotations.
// Use for run-scoped objects shared across workers (e.g. RSC).
func (f *Framework) stampRunMetadata(obj client.Object) {
	lbls := obj.GetLabels()
	if lbls == nil {
		lbls = make(map[string]string)
	}
	lbls[LabelE2ERunKey] = f.runID
	obj.SetLabels(lbls)

	ann := obj.GetAnnotations()
	if ann == nil {
		ann = make(map[string]string)
	}
	report := CurrentSpecReport()
	loc := report.LeafNodeLocation
	if loc.FileName != "" {
		ann[AnnotE2ESourceKey] = fmt.Sprintf("%s:%d",
			trimToRepoRelative(loc.FileName), loc.LineNumber)
	}
	if text := report.FullText(); text != "" {
		ann[AnnotE2ESpecKey] = text
	}
	obj.SetAnnotations(ann)
}

// stampMetadata sets run + worker labels and source/spec annotations.
// Use for worker-scoped objects (RV, RVR, RVA, DRBDR, LLV, etc.).
func (f *Framework) stampMetadata(obj client.Object) {
	f.stampRunMetadata(obj)
	lbls := obj.GetLabels()
	lbls[LabelE2EWorkerKey] = strconv.Itoa(f.WorkerID)
	obj.SetLabels(lbls)
}

// trimToRepoRelative strips the absolute path prefix, keeping everything
// from "e2e/" onward. Falls back to the base filename.
func trimToRepoRelative(abs string) string {
	if i := strings.Index(abs, "e2e/"); i >= 0 {
		return abs[i:]
	}
	return filepath.Base(abs)
}
