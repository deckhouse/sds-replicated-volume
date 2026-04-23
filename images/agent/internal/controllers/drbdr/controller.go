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
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/controlleroptions"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

// BuildController creates and registers the DRBD controller and scanner with the manager.
func BuildController(mgr manager.Manager) error {
	// Register field indexes
	if err := indexes.RegisterDRBDRByNodeName(mgr); err != nil {
		return fmt.Errorf("registering DRBDR index: %w", err)
	}
	if err := indexes.RegisterLVGByNodeName(mgr); err != nil {
		return fmt.Errorf("registering LVG index: %w", err)
	}
	if err := indexes.RegisterLLVByLVGName(mgr); err != nil {
		return fmt.Errorf("registering LLV index: %w", err)
	}

	cfg, err := env.GetConfig()
	if err != nil {
		return fmt.Errorf("getting config: %w", err)
	}

	cl := mgr.GetClient()
	nodeName := cfg.NodeName()

	// Set up drbd command logging
	origExec := drbdutils.ExecCommandContext
	drbdutils.ExecCommandContext = func(ctx context.Context, name string, arg ...string) drbdutils.Cmd {
		log.FromContext(ctx).Info("executing drbd command", "command", name, "args", arg)
		return origExec(ctx, name, arg...)
	}

	// Create internal request channel (scanner sends here)
	requestCh := make(chan event.TypedGenericEvent[DRBDReconcileRequest], 100)

	// Create DRBD port cache (scanner-maintained, will be used by PortRegistry later)
	drbdPortCache := NewDRBDPortCache()

	// Create scanner with port cache
	scanner := NewScanner(requestCh, drbdPortCache)
	if err := mgr.Add(scanner); err != nil {
		return fmt.Errorf("adding scanner runnable: %w", err)
	}

	// Create port registry (reconciler-owned, uses DRBDPortCache for kernel state)
	portRegistry := NewPortRegistry(cl, nodeName, drbdPortCache, cfg.DRBDMinPort(), cfg.DRBDMaxPort(), 10*time.Minute)

	// Create reconciler (implements reconcile.TypedReconciler[DRBDReconcileRequest])
	rec := NewReconciler(cl, nodeName, portRegistry)

	// Build DRBD resource controller with TypedReconciler
	if err := builder.TypedControllerManagedBy[DRBDReconcileRequest](mgr).
		Named(ControllerName).
		WithLogConstructor(func(req *DRBDReconcileRequest) logr.Logger {
			l := mgr.GetLogger().WithValues(
				"controller", ControllerName,
				"controllerGroup", v1alpha1.APIGroup,
				"controllerKind", "DRBDResource",
			)
			if req != nil {
				name := req.Name
				if name == "" {
					name = req.ActualNameOnTheNode
				}
				l = l.WithValues("name", name)
			}
			return l
		}).
		Watches(
			&v1alpha1.DRBDResource{},
			drbdrEventHandler(mgr.GetLogger().WithName("drbdr-watch"), nodeName),
			builder.WithPredicates(drbdrPredicates(nodeName)...),
		).
		// Watch internal channel (scanner events) - maps *DRBDReconcileRequest to DRBDReconcileRequest
		WatchesRawSource(
			source.TypedChannel(requestCh, handler.TypedEnqueueRequestsFromMapFunc(
				func(_ context.Context, req DRBDReconcileRequest) []DRBDReconcileRequest {
					return []DRBDReconcileRequest{req}
				},
			)),
		).
		WithOptions(controller.TypedOptions[DRBDReconcileRequest]{
			MaxConcurrentReconciles: 20,
			RateLimiter:             controlleroptions.DefaultRateLimiter[DRBDReconcileRequest](),
		}).
		Complete(withDurationLogging(rec)); err != nil {
		return fmt.Errorf("building DRBD resource controller: %w", err)
	}

	return nil
}

// drbdrEventHandler returns a typed event handler for DRBDResource watch that
// logs every Create/Update/Delete event with enough detail to attribute who
// mutated the object (managedFields, finalizers, deletionTimestamp, UID).
func drbdrEventHandler(baseLog logr.Logger, nodeName string) handler.TypedEventHandler[client.Object, DRBDReconcileRequest] {
	return handler.TypedFuncs[client.Object, DRBDReconcileRequest]{
		CreateFunc: func(_ context.Context, e event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[DRBDReconcileRequest]) {
			dr, ok := e.Object.(*v1alpha1.DRBDResource)
			if !ok || dr == nil {
				return
			}
			baseLog.Info("[drbdrEventHandler.CreateFunc] DRBDResource watch: CREATE",
				"name", dr.Name,
				"uid", string(dr.UID),
				"resourceVersion", dr.ResourceVersion,
				"specNodeName", dr.Spec.NodeName,
				"specState", string(dr.Spec.State),
				"finalizers", dr.Finalizers,
				"ownerRefs", ownerRefsSummary(dr.OwnerReferences),
				"managedFields", managedFieldsSummary(dr.ManagedFields),
				"watcherNode", nodeName,
			)
			q.Add(DRBDReconcileRequest{Name: dr.Name})
		},
		UpdateFunc: func(_ context.Context, e event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[DRBDReconcileRequest]) {
			newDR, okNew := e.ObjectNew.(*v1alpha1.DRBDResource)
			oldDR, okOld := e.ObjectOld.(*v1alpha1.DRBDResource)
			if !okNew || newDR == nil {
				return
			}
			oldDT := "<nil>"
			if okOld && oldDR != nil && oldDR.DeletionTimestamp != nil {
				oldDT = oldDR.DeletionTimestamp.String()
			}
			newDT := "<nil>"
			if newDR.DeletionTimestamp != nil {
				newDT = newDR.DeletionTimestamp.String()
			}
			if oldDT != newDT {
				baseLog.Info("[drbdrEventHandler.UpdateFunc] DRBDResource watch: UPDATE deletionTimestamp changed",
					"name", newDR.Name,
					"uid", string(newDR.UID),
					"oldDeletionTimestamp", oldDT,
					"newDeletionTimestamp", newDT,
					"finalizers", newDR.Finalizers,
					"managedFields", managedFieldsSummary(newDR.ManagedFields),
					"watcherNode", nodeName,
				)
			} else {
				baseLog.V(1).Info("[drbdrEventHandler.UpdateFunc] DRBDResource watch: UPDATE",
					"name", newDR.Name,
					"uid", string(newDR.UID),
					"deletionTimestamp", newDT,
					"finalizers", newDR.Finalizers,
				)
			}
			q.Add(DRBDReconcileRequest{Name: newDR.Name})
		},
		DeleteFunc: func(_ context.Context, e event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[DRBDReconcileRequest]) {
			dr, ok := e.Object.(*v1alpha1.DRBDResource)
			if !ok || dr == nil {
				return
			}
			dt := "<nil>"
			if dr.DeletionTimestamp != nil {
				dt = dr.DeletionTimestamp.String()
			}
			baseLog.Info("[drbdrEventHandler.DeleteFunc] DRBDResource watch: DELETE",
				"name", dr.Name,
				"uid", string(dr.UID),
				"resourceVersion", dr.ResourceVersion,
				"deletionTimestamp", dt,
				"finalizers", dr.Finalizers,
				"ownerRefs", ownerRefsSummary(dr.OwnerReferences),
				"managedFields", managedFieldsSummary(dr.ManagedFields),
				"deleteStateUnknown", e.DeleteStateUnknown,
				"watcherNode", nodeName,
			)
			q.Add(DRBDReconcileRequest{Name: dr.Name})
		},
		GenericFunc: func(_ context.Context, e event.TypedGenericEvent[client.Object], q workqueue.TypedRateLimitingInterface[DRBDReconcileRequest]) {
			dr, ok := e.Object.(*v1alpha1.DRBDResource)
			if !ok || dr == nil {
				return
			}
			q.Add(DRBDReconcileRequest{Name: dr.Name})
		},
	}
}

// ownerRefsSummary reduces OwnerReferences to a compact printable form.
func ownerRefsSummary(refs []metav1.OwnerReference) []string {
	out := make([]string, 0, len(refs))
	for i := range refs {
		ctrl := false
		if refs[i].Controller != nil {
			ctrl = *refs[i].Controller
		}
		out = append(out, fmt.Sprintf("%s/%s(uid=%s,ctrl=%t)", refs[i].Kind, refs[i].Name, string(refs[i].UID), ctrl))
	}
	return out
}

// managedFieldsSummary reduces ManagedFields to (manager, operation, time, subresource) tuples.
func managedFieldsSummary(mfs []metav1.ManagedFieldsEntry) []string {
	out := make([]string, 0, len(mfs))
	for i := range mfs {
		ts := ""
		if mfs[i].Time != nil {
			ts = mfs[i].Time.String()
		}
		out = append(out, fmt.Sprintf("manager=%q op=%s sub=%s t=%s", mfs[i].Manager, mfs[i].Operation, mfs[i].Subresource, ts))
	}
	return out
}

func withDurationLogging(inner reconcile.TypedReconciler[DRBDReconcileRequest]) reconcile.TypedReconciler[DRBDReconcileRequest] {
	return reconcile.TypedFunc[DRBDReconcileRequest](func(ctx context.Context, req DRBDReconcileRequest) (reconcile.Result, error) {
		start := time.Now()
		res, err := inner.Reconcile(ctx, req)
		log.FromContext(ctx).Info("Reconcile complete", "duration", time.Since(start).String())
		return res, err
	})
}
