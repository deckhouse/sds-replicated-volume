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

package rvstatusconfigdeviceminor

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	// RVStatusConfigDeviceMinorControllerName is the controller name for rv_status_config_device_minor controller.
	RVStatusConfigDeviceMinorControllerName = "rv_status_config_device_minor_controller"
)

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()

	log := mgr.GetLogger().WithName(RVStatusConfigDeviceMinorControllerName)

	dmCache, err := initializeDMCache(context.Background(), cl)
	if err != nil {
		return err
	}

	log.Info("initialized device minor cache",
		"len", dmCache.Len(),
		"max", dmCache.Max(),
		"releasedLen", dmCache.ReleasedLen(),
	)

	rec := NewReconciler(
		cl,
		log.WithName("Reconciler"),
		dmCache,
	)

	return builder.ControllerManagedBy(mgr).
		Named(RVStatusConfigDeviceMinorControllerName).
		For(
			&v1alpha1.ReplicatedVolume{},
			builder.WithPredicates(
				predicate.Funcs{
					CreateFunc: func(e event.TypedCreateEvent[client.Object]) bool {
						return true
					},
					UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
						// deviceMinor can only be changed once, by this controller
						return false
					},
					DeleteFunc: func(e event.TypedDeleteEvent[client.Object]) bool {
						dmCache.Release(e.Object.GetName())
						return false
					},
					GenericFunc: func(event.TypedGenericEvent[client.Object]) bool {
						return false
					},
				},
			),
		).
		// MaxConcurrentReconciles: 1
		// prevents race conditions when assigning unique deviceMinor values
		// to different ReplicatedVolume resources
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(rec)
}

func deviceMinor(rv *v1alpha1.ReplicatedVolume) (int, bool) {
	if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil && rv.Status.DRBD.Config.DeviceMinor != nil {
		return int(*rv.Status.DRBD.Config.DeviceMinor), true
	}
	return 0, false
}

func initializeDMCache(ctx context.Context, cl client.Client) (*DeviceMinorCache, error) {
	dmCache := NewDeviceMinorCache()

	rvList := &v1alpha1.ReplicatedVolumeList{}
	if err := cl.List(ctx, rvList); err != nil {
		return nil, fmt.Errorf("listing rvs: %w", err)
	}

	rvByName := make(map[string]*v1alpha1.ReplicatedVolume, len(rvList.Items))
	dmByRVName := make(map[string]DeviceMinor, len(rvList.Items))

	for i := range rvList.Items {
		rv := &rvList.Items[i]
		rvByName[rv.Name] = rv

		deviceMinorVal, isSet := deviceMinor(rv)
		if !isSet {
			continue
		}

		dm, valid := NewDeviceMinor(deviceMinorVal)
		if !valid {
			return nil, fmt.Errorf("invalid device minor for rv %s: %d", rv.Name, rv.Status.DRBD.Config.DeviceMinor)
		}

		dmByRVName[rv.Name] = dm
	}

	if initErr := dmCache.Initialize(dmByRVName); initErr != nil {
		if dupErr, ok := initErr.(DuplicateDeviceMinorError); ok {
			for _, rvName := range dupErr.ConflictingRVNames {
				if err := patchDupErr(ctx, cl, rvByName[rvName], dupErr.ConflictingRVNames); err != nil {
					initErr = errors.Join(initErr, err)
				}
			}
		}
		return nil, fmt.Errorf("initializing device minor cache: %w", initErr)
	}

	return dmCache, nil
}
