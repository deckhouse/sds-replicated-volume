package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/deckhouse/sds-common-lib/slogh"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	r "github.com/deckhouse/sds-replicated-volume/images/agent/internal/reconcile"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/reconcile/drbdresourcereplica"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

func main() {
	ctx := signals.SetupSignalHandler()

	logHandler := slogh.NewHandler(slogh.Config{})
	log := slog.New(logHandler)
	crlog.SetLogger(logr.FromSlogHandler(logHandler))

	config, err := config.GetConfig()
	if err != nil {
		log.Error("getting rest config", slog.Any("error", err))
		os.Exit(1)
	}

	scheme, err := newScheme()
	if err != nil {
		log.Error("building scheme", slog.Any("error", err))
		os.Exit(1)
	}

	mgrOpts := manager.Options{
		Scheme: scheme,
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&v1alpha1.DRBDResource{}: {
					Namespaces: map[string]cache.Config{
						"my": {
							LabelSelector: labels.SelectorFromSet(labels.Set{"abc": "asd"}),
						},
					},
				},
			},
		},
		BaseContext: func() context.Context { return ctx },
	}

	mgr, err := manager.New(config, mgrOpts)
	if err != nil {
		log.Error("creating manager", slog.Any("error", err))
		os.Exit(1)
	}

	ctrlLog := log.With("controller", "drbdresource")

	err = builder.TypedControllerManagedBy[r.TypedRequest[*v1alpha2.DRBDResourceReplica]](mgr).
		Watches(
			&v1alpha2.DRBDResourceReplica{},
			&handler.TypedFuncs[client.Object, r.TypedRequest[*v1alpha2.DRBDResourceReplica]]{
				CreateFunc: func(
					ctx context.Context,
					ce event.TypedCreateEvent[client.Object],
					q workqueue.TypedRateLimitingInterface[r.TypedRequest[*v1alpha2.DRBDResourceReplica]],
				) {
					ctrlLog.Debug("CreateFunc", slog.Group("object", "name", ce.Object.GetName()))
					typedObj := ce.Object.(*v1alpha2.DRBDResourceReplica)
					q.Add(r.NewTypedRequestCreate(typedObj))
				},
				UpdateFunc: func(
					ctx context.Context,
					ue event.TypedUpdateEvent[client.Object],
					q workqueue.TypedRateLimitingInterface[r.TypedRequest[*v1alpha2.DRBDResourceReplica]],
				) {
					ctrlLog.Debug(
						"UpdateFunc",
						slog.Group("objectNew", "name", ue.ObjectNew.GetName()),
						slog.Group("objectOld", "name", ue.ObjectOld.GetName()),
					)
					typedObjOld := ue.ObjectOld.(*v1alpha2.DRBDResourceReplica)
					typedObjNew := ue.ObjectNew.(*v1alpha2.DRBDResourceReplica)
					q.Add(r.NewTypedRequestUpdate(typedObjOld, typedObjNew))
				},
				DeleteFunc: func(
					ctx context.Context,
					de event.TypedDeleteEvent[client.Object],
					q workqueue.TypedRateLimitingInterface[r.TypedRequest[*v1alpha2.DRBDResourceReplica]],
				) {
					ctrlLog.Debug("DeleteFunc", slog.Group("object", "name", de.Object.GetName()))
					typedObj := de.Object.(*v1alpha2.DRBDResourceReplica)
					q.Add(r.NewTypedRequestDelete(typedObj))
				},
				GenericFunc: func(
					ctx context.Context,
					ge event.TypedGenericEvent[client.Object],
					q workqueue.TypedRateLimitingInterface[r.TypedRequest[*v1alpha2.DRBDResourceReplica]],
				) {
					ctrlLog.Debug("GenericFunc - skipping", slog.Group("object", "name", ge.Object.GetName()))
				},
			}).
		Complete(drbdresourcereplica.NewReconciler(ctrlLog))

	if err != nil {
		log.Error("starting controller", slog.Any("error", err))
		os.Exit(1)
	}

}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	var schemeFuncs = []func(s *runtime.Scheme) error{
		corev1.AddToScheme,
		storagev1.AddToScheme,
		v1alpha1.AddToScheme,
	}

	for i, f := range schemeFuncs {
		if err := f(scheme); err != nil {
			return nil, fmt.Errorf("adding scheme %d: %w", i, err)
		}
	}

	return scheme, nil
}
