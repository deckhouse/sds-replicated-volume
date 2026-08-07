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

package drbdr

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// drbdMapperClient is one reconcile's view of the single DRBDMapper layered on a
// DRBDResource's device symlink. It remembers what it read and what it wrote, so the
// steps that run after a create or a delete see the result instead of the informer
// cache, which lags both.
//
// It must not outlive the reconcile that made it.
type drbdMapperClient struct {
	cl              client.Client
	name            string
	nodeName        string
	lowerDevicePath string

	drbdm  *v1alpha1.DRBDMapper
	loaded bool
}

func newDRBDMapperClient(cl client.Client, nodeName, drbdrName string) *drbdMapperClient {
	return &drbdMapperClient{
		cl:              cl,
		name:            drbdrName,
		nodeName:        nodeName,
		lowerDevicePath: v1alpha1.FormatDRBDResourceDeviceSymlinkPath(drbdrName),
	}
}

// Get returns the DRBDMapper, or nil when there is none.
func (c *drbdMapperClient) Get(ctx context.Context) (*v1alpha1.DRBDMapper, error) {
	if c.loaded {
		return c.drbdm, nil
	}

	list := &v1alpha1.DRBDMapperList{}
	if err := c.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldDRBDMByLowerDevicePath: c.lowerDevicePath,
	}); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].Spec.NodeName == c.nodeName {
			c.drbdm = &list.Items[i]
			break
		}
	}

	c.loaded = true
	return c.drbdm, nil
}

// Create creates the DRBDMapper drbdm builds the consumer's dm layers from.
func (c *drbdMapperClient) Create(ctx context.Context) error {
	drbdm := &v1alpha1.DRBDMapper{
		ObjectMeta: metav1.ObjectMeta{Name: c.name},
		Spec: v1alpha1.DRBDMapperSpec{
			NodeName:        c.nodeName,
			LowerDevicePath: c.lowerDevicePath,
		},
	}
	if err := c.cl.Create(ctx, drbdm); err != nil {
		return flow.Wrapf(err, "creating DRBDMapper %q", c.name)
	}

	c.drbdm, c.loaded = drbdm, true
	return nil
}

// Delete deletes the DRBDMapper. drbdm holds it through its finalizer until the dm
// layers are gone, so Get keeps returning it, marked deleting.
func (c *drbdMapperClient) Delete(ctx context.Context) error {
	drbdm, err := c.Get(ctx)
	if err != nil {
		return err
	}
	if drbdm == nil || drbdm.DeletionTimestamp != nil {
		return nil
	}

	if err := client.IgnoreNotFound(c.cl.Delete(ctx, drbdm)); err != nil {
		return flow.Wrapf(err, "deleting DRBDMapper %q", c.name)
	}

	drbdm.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
}

// reportDRBDMapperDevice publishes the device a consumer uses. That is the
// DRBDMapper's upper device, not the DRBD device below it, so the values come from
// the DRBDMapper and survive the DRBD resource being down for a module upgrade.
//
// Reconcile pattern: Report
func reportDRBDMapperDevice(status *v1alpha1.DRBDResourceStatus, drbdm *v1alpha1.DRBDMapper) {
	if drbdm == nil || !obju.IsStatusConditionPresentAndTrue(drbdm, v1alpha1.DRBDMapperCondConfiguredType) {
		status.Device = ""
		status.DeviceOpen = nil
		status.DeviceIOSuspended = nil
		return
	}

	status.Device = drbdm.Status.UpperDevicePath
	status.DeviceOpen = ptr.To(drbdm.Status.OpenCount > 0)
	status.DeviceIOSuspended = ptr.To(
		obju.IsStatusConditionPresentAndTrue(drbdm, v1alpha1.DRBDMapperCondIOSuspendedType),
	)
}
