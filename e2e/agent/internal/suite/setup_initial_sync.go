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

package suite

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupInitialSync clears the initial sync bitmap via CreateNewUUID and waits
// for all replicas to reach DiskState=UpToDate.
func SetupInitialSync(
	e envtesting.E,
	cl client.WithWatch,
	drbdrs []*v1alpha1.DRBDResource,
) {
	var opTimeout DRBDROpTimeout
	var diskTimeout DiskUpToDateTimeout
	e.Options(&opTimeout, &diskTimeout)

	SetupDRBDResourceOperation(
		e.ScopeWithTimeout(opTimeout.Duration), cl,
		newCreateNewUUIDClearBitmap(drbdrs[0].Name+"-sync", drbdrs[0].Name),
	)

	type waitFn = func(envtesting.E, *v1alpha1.DRBDResource, func(*v1alpha1.DRBDResource) bool) *v1alpha1.DRBDResource

	watcherScope := e.Scope()
	defer watcherScope.Close()
	waitFns := make([]waitFn, len(drbdrs))
	for i, drbdr := range drbdrs {
		waitFns[i] = kubetesting.SetupResourceWatcher(watcherScope, cl, drbdr)
	}

	uptodateScope := e.ScopeWithTimeout(diskTimeout.Duration)
	defer uptodateScope.Close()

	for i := range drbdrs {
		drbdrs[i] = waitFns[i](uptodateScope, drbdrs[i], isDiskUpToDate)
		assertDiskUpToDate(e, drbdrs[i])
	}
}

func newCreateNewUUIDClearBitmap(name, drbdrName string) *v1alpha1.DRBDResourceOperation {
	return &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: drbdrName,
			Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
			CreateNewUUID:    &v1alpha1.CreateNewUUIDParams{ClearBitmap: true},
		},
	}
}

func isDiskUpToDate(drbdr *v1alpha1.DRBDResource) bool {
	return drbdr.Status.DiskState == v1alpha1.DiskStateUpToDate
}

func assertDiskUpToDate(e envtesting.E, drbdr *v1alpha1.DRBDResource) {
	if drbdr.Status.DiskState != v1alpha1.DiskStateUpToDate {
		e.Fatalf("assert: DRBDResource %q DiskState is %s, want UpToDate",
			drbdr.Name, drbdr.Status.DiskState)
	}
}
