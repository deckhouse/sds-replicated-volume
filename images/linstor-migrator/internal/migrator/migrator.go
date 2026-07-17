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

package migrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"

	d8commonapi "github.com/deckhouse/sds-common-lib/api/v1alpha1"
	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/kubeutils"
)

const (
	// DefaultRetryInterval is the default wait between migrator retry rounds (ConfigMap state updates, stage 2 polling).
	DefaultRetryInterval = 2 * time.Second
	// DefaultStage2WorkerCount is the default number of parallel stage 2 workers.
	DefaultStage2WorkerCount = 5
)

// MigratorOptions configures optional migrator behavior. Zero values in RetryInterval,
// RetryBackoff, and Stage2WorkerCount are replaced with defaults when passed to New.
type MigratorOptions struct {
	RetryInterval     time.Duration
	RetryBackoff      wait.Backoff
	Stage2WorkerCount int
}

// Migrator performs the migration from LINSTOR CRs to the new control-plane CRs.
type Migrator struct {
	client            kubecl.Client
	dynamicClient     dynamic.Interface
	log               *slog.Logger
	retryInterval     time.Duration
	retryBackoff      wait.Backoff
	stage2WorkerCount int
}

// New creates a new Migrator instance.
func New(client kubecl.Client, dynamicClient dynamic.Interface, log *slog.Logger, opts MigratorOptions) *Migrator {
	if opts.RetryInterval <= 0 {
		opts.RetryInterval = DefaultRetryInterval
	}
	if opts.RetryBackoff.Duration <= 0 {
		opts.RetryBackoff = kubeutils.DefaultRetryBackoff
	}
	if opts.Stage2WorkerCount <= 0 {
		opts.Stage2WorkerCount = DefaultStage2WorkerCount
	}
	return &Migrator{
		client:            client,
		dynamicClient:     dynamicClient,
		log:               log,
		retryInterval:     opts.RetryInterval,
		retryBackoff:      opts.RetryBackoff,
		stage2WorkerCount: opts.Stage2WorkerCount,
	}
}

// Run executes the full migration workflow: pre-flight checks, stage 1 (resource migration),
// stage 2 (adopt exit maintenance), and stage 3 (LINSTOR cleanup).
func (m *Migrator) Run(ctx context.Context) error {
	if err := m.checkNewControlPlaneEnabled(ctx); err != nil {
		return err
	}

	found, err := m.crdExistsWithRetry(ctx, config.NewControlPlaneCRDName, "new control-plane")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("new control-plane CRD %q not found in cluster; ensure the new control-plane is installed before running migration", config.NewControlPlaneCRDName)
	}
	m.log.Debug("new control-plane CRD found", "crd", config.NewControlPlaneCRDName)

	currentState, err := m.ensureConfigMap(ctx)
	if err != nil {
		return fmt.Errorf("failed to ensure migration ConfigMap: %w", err)
	}

	switch currentState {
	case config.StateAllCompleted:
		m.log.Info("migration already completed, nothing to do")
		return nil
	case config.StateStage2Completed, config.StateStage3Started:
		return m.runStage3(ctx)
	case config.StateStage1Completed, config.StateStage2Started:
		return m.runFromStage2(ctx)
	default:
		return m.runFromStage1(ctx)
	}
}

func (m *Migrator) runFromStage1(ctx context.Context) error {
	if err := m.runStage1(ctx); err != nil {
		return err
	}
	return m.runFromStage2(ctx)
}

func (m *Migrator) runFromStage2(ctx context.Context) error {
	if err := m.runStage2(ctx); err != nil {
		return err
	}
	return m.runStage3(ctx)
}

// ensureConfigMap creates the migration ConfigMap if it does not exist and returns the current state.
// The entire ensure sequence is wrapped in retryTransient: a transient error on Get or Create is
// retried by re-running the whole function. AlreadyExists (HTTP 409, classified as transient) and
// NotFound are swallowed inside fn so they are not retried forever.
func (m *Migrator) ensureConfigMap(ctx context.Context) (string, error) {
	var currentState string
	var created bool
	err := m.retryTransient(ctx, "ensure migration ConfigMap", func() error {
		cm := &corev1.ConfigMap{}
		gerr := m.client.Get(ctx, types.NamespacedName{
			Namespace: config.ModuleNamespace,
			Name:      config.MigrationConfigMapName,
		}, cm)

		if gerr == nil {
			currentState = cm.Data["state"]
			if currentState == "" {
				currentState = config.StateNotStarted
			}
			created = false
			return nil
		}
		if !apierrors.IsNotFound(gerr) {
			return gerr
		}

		// NotFound: create the ConfigMap.
		newCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.MigrationConfigMapName,
				Namespace: config.ModuleNamespace,
			},
			Data: map[string]string{
				"state": config.StateNotStarted,
			},
		}
		cerr := m.client.Create(ctx, newCM)
		if cerr != nil && apierrors.IsAlreadyExists(cerr) {
			// Another instance created it concurrently, or the Create response was lost and
			// retried. Re-Get the actual ConfigMap to recover the real migration state instead
			// of assuming StateNotStarted — the existing object may hold advanced progress
			// (e.g. stage2-completed) that must not be lost by re-entering stage 1.
			if gerr2 := m.client.Get(ctx, types.NamespacedName{
				Namespace: config.ModuleNamespace,
				Name:      config.MigrationConfigMapName,
			}, cm); gerr2 != nil {
				return gerr2
			}
			currentState = cm.Data["state"]
			if currentState == "" {
				currentState = config.StateNotStarted
			}
			created = false
			return nil
		}
		if cerr != nil {
			return cerr
		}
		currentState = config.StateNotStarted
		created = true
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to ensure migration ConfigMap %s/%s: %w", config.ModuleNamespace, config.MigrationConfigMapName, err)
	}
	if created {
		m.log.Info("created migration ConfigMap", "namespace", config.ModuleNamespace, "name", config.MigrationConfigMapName)
	} else {
		m.log.Debug("migration ConfigMap exists", "state", currentState)
	}
	return currentState, nil
}

// updateMigrationState patches the migration ConfigMap with the new state.
func (m *Migrator) updateMigrationState(ctx context.Context, state string) error {
	cm := &corev1.ConfigMap{}
	if err := m.client.Get(ctx, types.NamespacedName{
		Namespace: config.ModuleNamespace,
		Name:      config.MigrationConfigMapName,
	}, cm); err != nil {
		return fmt.Errorf("failed to get ConfigMap for state update: %w", err)
	}

	return m.patchConfigMapState(ctx, cm, state)
}

// updateMigrationStateRetrying patches ConfigMap state, retrying transient API errors.
func (m *Migrator) updateMigrationStateRetrying(ctx context.Context, state string) error {
	return m.retryTransient(ctx, "update migration state", func() error {
		if err := m.updateMigrationState(ctx, state); err != nil {
			return fmt.Errorf("failed to update migration state to %q: %w", state, err)
		}
		return nil
	})
}

// retryTransient runs fn until it succeeds or returns a non-transient error, using exponential
// backoff with jitter. It is the migrator-local wrapper around kubeutils.RetryTransient so all
// stages share one retry strategy. The retry is unbounded: migration is expected to complete
// eventually; cancellation is honoured via ctx.
func (m *Migrator) retryTransient(ctx context.Context, label string, fn func() error) error {
	return kubeutils.RetryTransient(ctx, m.log, m.retryBackoff, label, fn)
}

// patchConfigMapState applies a merge patch to update the state field.
func (m *Migrator) patchConfigMapState(ctx context.Context, cm *corev1.ConfigMap, state string) error {
	oldCM := cm.DeepCopy()
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data["state"] = state
	if err := m.client.Patch(ctx, cm, kubecl.MergeFrom(oldCM)); err != nil {
		return fmt.Errorf("failed to patch ConfigMap state to %q: %w", state, err)
	}
	m.log.Info("migration state updated in the ConfigMap", "state", state)
	return nil
}

// crdExists checks if a CRD with the given name exists in the cluster.
func (m *Migrator) crdExists(ctx context.Context, crdName string) error {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	return m.client.Get(ctx, types.NamespacedName{Name: crdName}, crd)
}

// crdExistsWithRetry checks whether the named CRD exists, retrying transient API errors.
// It returns (found, error): found=true means the CRD exists, found=false means it is NotFound.
// NotFound is NOT a transient error in the classifier, so it is swallowed inside the closure
// and reported via the bool rather than terminating the retry loop.
// The label is used in retry-log lines and in the returned error message for identification.
func (m *Migrator) crdExistsWithRetry(ctx context.Context, name, label string) (bool, error) {
	var notFound bool
	if err := m.retryTransient(ctx, "check "+label, func() error {
		err := m.crdExists(ctx, name)
		if err == nil {
			notFound = false
			return nil
		}
		if apierrors.IsNotFound(err) {
			notFound = true
			return nil
		}
		return err
	}); err != nil {
		return false, fmt.Errorf("failed to check %s CRD %q: %w", label, name, err)
	}
	return !notFound, nil
}

// checkNewControlPlaneEnabled verifies that newControlPlane is set to true in ModuleConfig.
func (m *Migrator) checkNewControlPlaneEnabled(ctx context.Context) error {
	mc := &d8commonapi.ModuleConfig{}
	if err := m.retryTransient(ctx, "check newControlPlane enabled", func() error {
		return m.client.Get(ctx, kubecl.ObjectKey{Name: config.ModuleName}, mc)
	}); err != nil {
		return fmt.Errorf("failed to get ModuleConfig %q: %w", config.ModuleName, err)
	}

	if value, exists := mc.Spec.Settings["newControlPlane"]; exists && value == true {
		m.log.Debug("new control-plane is enabled in ModuleConfig")
		return nil
	}

	return fmt.Errorf("ModuleConfig %q has newControlPlane=false; enable the new control-plane by setting spec.settings.newControlPlane to true before running migration", config.ModuleName)
}

// csiControllerFinalizers returns the CSI controller finalizer set on ReplicatedVolume
// and ReplicatedVolumeAttachment at create time (same as images/csi-driver).
func csiControllerFinalizers() []string {
	return []string{srvv1alpha1.CSIControllerFinalizer}
}

// createIfNotExists creates a Kubernetes resource if it does not already exist.
// It is idempotent: if the resource already exists, it logs and returns nil.
// Pass a logger from log.With(...) so Info/Debug lines include stable identifying attributes (see ensureMigrationRSP).
func (m *Migrator) createIfNotExists(ctx context.Context, log *slog.Logger, obj kubecl.Object, kind string) error {
	if err := m.client.Create(ctx, obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Debug("resource already exists, skipping", "kind", kind, "name", obj.GetName())
			return nil
		}
		return fmt.Errorf("failed to create %s %q: %w", kind, obj.GetName(), err)
	}
	log.Info("created resource", "kind", kind, "name", obj.GetName())
	return nil
}
