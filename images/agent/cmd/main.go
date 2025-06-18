package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/deckhouse/sds-common-lib/slogh"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	r "github.com/deckhouse/sds-replicated-volume/images/agent/internal/reconcile"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/reconcile/rvr"

	//lint:ignore ST1001 utils is the only exception
	. "github.com/deckhouse/sds-replicated-volume/images/agent/internal/utils"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
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

	log.Info("agent started")

	err := runAgent(ctx, log)
	if !errors.Is(err, context.Canceled) || ctx.Err() != context.Canceled {
		// errors should already be logged
		os.Exit(1)
	}
	log.Info(
		"agent gracefully shutdown",
		// cleanup errors do not affect status code, but worth logging
		"err", err,
	)
}

func runAgent(ctx context.Context, log *slog.Logger) (err error) {
	// to be used in goroutines spawned below
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() { cancel(err) }()

	hostname, err := os.Hostname()
	if err != nil {
		return LogError(log, fmt.Errorf("getting hostname: %w", err))
	}
	log = log.With("hostname", hostname)

	// MANAGER
	mgr, err := newManager(ctx, log, hostname)
	if err != nil {
		return err
	}

	cl := mgr.GetClient()

	// DRBD SCANNER
	go func() {
		var err error
		defer func() { cancel(fmt.Errorf("drbdsetup scanner: %w", err)) }()
		defer RecoverPanicToErr(&err)
		err = NewScanner(ctx, log, cl, hostname).Run()
	}()

	// CONTROLLERS
	go func() {
		var err error
		defer func() { cancel(fmt.Errorf("rvr controller: %w", err)) }()
		defer RecoverPanicToErr(&err)
		err = runController(log, mgr)
	}()

	<-ctx.Done()

	return context.Cause(ctx)
}

func newManager(
	ctx context.Context,
	log *slog.Logger,
	hostname string,
) (manager.Manager, error) {
	config, err := config.GetConfig()
	if err != nil {
		return nil, LogError(log, fmt.Errorf("getting rest config: %w", err))
	}

	scheme, err := newScheme()
	if err != nil {
		return nil, LogError(log, fmt.Errorf("building scheme: %w", err))
	}

	mgrOpts := manager.Options{
		Scheme:      scheme,
		BaseContext: func() context.Context { return ctx },
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&v1alpha2.ReplicatedVolumeReplica{}: {
					// only watch current node's replicas
					Field: (&v1alpha2.ReplicatedVolumeReplica{}).
						NodeNameSelector(hostname),
				},
			},
		},
	}

	mgr, err := manager.New(config, mgrOpts)
	if err != nil {
		return nil, LogError(log, fmt.Errorf("creating manager: %w", err))
	}

	// err = mgr.GetFieldIndexer().IndexField(
	// 	ctx,
	// 	&v1alpha2.ReplicatedVolumeReplica{},
	// 	(&v1alpha2.ReplicatedVolumeReplica{}).UniqueIndexName(),
	// 	func(o client.Object) []string {
	// 		rr := o.(*v1alpha2.ReplicatedVolumeReplica)
	// 		key := rr.UniqueIndexKey()
	// 		if key == "" {
	// 			return nil
	// 		}
	// 		return []string{key}
	// 	},
	// )
	// if err != nil {
	// 	return nil,
	// 		LogError(
	// 			log,
	// 			fmt.Errorf(
	// 				"indexing %s: %w",
	// 				reflect.TypeFor[v1alpha2.ReplicatedVolumeReplica]().Name(),
	// 				err,
	// 			),
	// 		)
	// }

	return mgr, nil
}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	var schemeFuncs = []func(s *runtime.Scheme) error{
		corev1.AddToScheme,
		storagev1.AddToScheme,
		v1alpha2.AddToScheme,
	}

	for i, f := range schemeFuncs {
		if err := f(scheme); err != nil {
			return nil, fmt.Errorf("adding scheme %d: %w", i, err)
		}
	}

	return scheme, nil
}

func runController(
	log *slog.Logger,
	mgr manager.Manager,
) error {
	log = log.With("goroutine", "controller")

	type TReq = r.TypedRequest[*v1alpha2.ReplicatedVolumeReplica]
	type TQueue = workqueue.TypedRateLimitingInterface[TReq]

	err := builder.TypedControllerManagedBy[TReq](mgr).
		Named("replicatedVolumeReplica").
		Watches(
			&v1alpha2.ReplicatedVolumeReplica{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					ctx context.Context,
					ce event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					log.Debug(
						"CreateFunc",
						slog.Group("object", "name", ce.Object.GetName()),
					)
					typedObj := ce.Object.(*v1alpha2.ReplicatedVolumeReplica)
					q.Add(r.NewTypedRequestCreate(typedObj))
				},
				UpdateFunc: func(
					ctx context.Context,
					ue event.TypedUpdateEvent[client.Object],
					q TQueue,
				) {
					log.Debug(
						"UpdateFunc",
						slog.Group("objectNew", "name", ue.ObjectNew.GetName()),
						slog.Group("objectOld", "name", ue.ObjectOld.GetName()),
					)
					typedObjOld := ue.ObjectOld.(*v1alpha2.ReplicatedVolumeReplica)
					typedObjNew := ue.ObjectNew.(*v1alpha2.ReplicatedVolumeReplica)

					// skip status and metadata updates
					if typedObjOld.Generation == typedObjNew.Generation {
						return
					}

					q.Add(r.NewTypedRequestUpdate(typedObjOld, typedObjNew))
				},
				DeleteFunc: func(
					ctx context.Context,
					de event.TypedDeleteEvent[client.Object],
					q TQueue,
				) {
					log.Debug(
						"DeleteFunc",
						slog.Group("object", "name", de.Object.GetName()),
					)
					typedObj := de.Object.(*v1alpha2.ReplicatedVolumeReplica)
					q.Add(r.NewTypedRequestDelete(typedObj))
				},
				GenericFunc: func(
					ctx context.Context,
					ge event.TypedGenericEvent[client.Object],
					q TQueue,
				) {
					log.Debug(
						"GenericFunc - skipping",
						slog.Group("object", "name", ge.Object.GetName()),
					)
				},
			}).
		Complete(rvr.NewReconciler(log))

	if err != nil {
		return LogError(log, fmt.Errorf("running controller: %w", err))
	}

	return nil
}
