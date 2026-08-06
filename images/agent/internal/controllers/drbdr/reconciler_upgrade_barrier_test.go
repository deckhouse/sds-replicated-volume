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

package drbdr_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdr"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/upgrade"
	fakedrbdutils "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils/fake"
	commonsync "github.com/deckhouse/sds-replicated-volume/lib/go/common/sync"
)

// A pending upgrade must gate the whole reconcile: nothing may touch DRBD before it
// succeeds, and its failure has to reach the caller so the request is requeued.
func TestReconcileBlocksOnPendingModuleUpgrade(t *testing.T) {
	original := upgrade.Upgrader
	t.Cleanup(func() { upgrade.Upgrader = original })

	upgradeErr := errors.New("module reload failed")
	upgraded := false
	upgrade.Upgrader = commonsync.NewOnceUpgrader(true, func(context.Context) error {
		upgraded = true
		return upgradeErr
	})

	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}

	drbdr.OverrideDeviceSymlinkDir(t.TempDir() + "/")

	drbdrObj := drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp)
	cl := fake.NewClientBuilder().
		WithScheme(sch).
		WithStatusSubresource(&v1alpha1.DRBDResource{}).
		WithIndex(&v1alpha1.DRBDResource{}, indexes.IndexFieldDRBDRByNodeName, func(obj client.Object) []string {
			dr, ok := obj.(*v1alpha1.DRBDResource)
			if !ok || dr.Spec.NodeName == "" {
				return nil
			}
			return []string{dr.Spec.NodeName}
		}).
		WithObjects(drbdrObj, testNode(testNodeName)).
		Build()

	// A blocked reconcile must issue none; the fake fails the test on any call.
	fakeExec := &fakedrbdutils.Exec{}
	fakeExec.ExpectCommands()
	fakeExec.Setup(t)

	drbdPortCache := drbdr.NewDRBDPortCache()
	drbdPortCache.BeginDump()
	drbdPortCache.EndDump()
	portRegistry := drbdr.NewPortRegistry(cl, testNodeName, drbdPortCache, 7000, 7999, 10*time.Minute)
	caches := drbdr.NewCaches()
	caches.SeedStatusAbsentForTest(testDRBDResName, testCustomDRBDName)

	rec := drbdr.NewReconciler(cl, testNodeName, portRegistry, caches)
	_, err = rec.Reconcile(t.Context(), drbdr.DRBDReconcileRequest{Name: drbdrObj.Name})

	if !upgraded {
		t.Error("the reconcile did not consult the upgrade barrier")
	}
	if !errors.Is(err, upgradeErr) {
		t.Errorf("Reconcile() error = %v; want it to wrap %v", err, upgradeErr)
	}
}
