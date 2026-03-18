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
	"context"
	"fmt"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// scavengeAllE2E deletes ALL objects with the e2e run label.
// Fire-and-forget: no debugger, no waiting. Used in BeforeSuite
// (to clean previous runs) and in SynchronizedAfterSuite process-1
// (to catch RSCs and any stragglers).
func (f *Framework) scavengeAllE2E(ctx context.Context) {
	if f.Client == nil {
		return
	}

	reqExists, err := labels.NewRequirement(LabelE2ERunKey, selection.Exists, nil)
	if err != nil {
		return
	}
	sel := labels.NewSelector().Add(*reqExists)
	opts := &client.DeleteAllOfOptions{}
	opts.LabelSelector = sel

	for _, obj := range e2eTypes() {
		if err := f.Client.DeleteAllOf(ctx, obj, opts); err != nil {
			fmt.Fprintf(GinkgoWriter, "[%s] scavenge: failed to delete %T: %v\n",
				time.Now().Format("15:04:05.000"), obj, err)
		}
	}

	fmt.Fprintf(GinkgoWriter, "[%s] scavenge: fired delete for all e2e-labeled objects\n",
		time.Now().Format("15:04:05.000"))
}

// cleanupWorkerObjects is the per-worker AfterSuite safety net. It deletes
// all objects belonging to this run+worker, then polls until they are gone.
func (f *Framework) cleanupWorkerObjects(ctx context.Context) {
	if f.Client == nil {
		return
	}

	sel := client.MatchingLabels{
		LabelE2ERunKey:    f.runID,
		LabelE2EWorkerKey: strconv.Itoa(f.WorkerID),
	}

	found := f.countByLabel(ctx, sel)
	if found == 0 {
		return
	}

	fmt.Fprintf(GinkgoWriter, "[%s] OWN CLEANUP: %d object(s) with run=%s worker=%d\n",
		time.Now().Format("15:04:05.000"), found, f.runID, f.WorkerID)

	for _, obj := range e2eTypes() {
		_ = f.Client.DeleteAllOf(ctx, obj, sel)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		if f.countByLabel(ctx, sel) == 0 {
			break
		}
	}

	fmt.Fprintf(GinkgoWriter, "[%s] OWN CLEANUP: done\n",
		time.Now().Format("15:04:05.000"))
}

// countByLabel counts all e2e-managed objects matching the given label selector.
func (f *Framework) countByLabel(ctx context.Context, sel client.MatchingLabels) int {
	count := 0
	for _, list := range e2eListTypes() {
		if err := f.Client.List(ctx, list, sel); err != nil {
			continue
		}
		items, _ := meta.ExtractList(list)
		count += len(items)
	}
	return count
}
