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
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"

	d8commonapi "github.com/deckhouse/sds-common-lib/api/v1alpha1"
	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

const (
	// DefaultRetryInterval is the default wait between migrator retry rounds (ConfigMap state updates, stage 2 polling).
	DefaultRetryInterval = 2 * time.Second
	// DefaultStage2WorkerCount is the default number of parallel stage 2 workers.
	DefaultStage2WorkerCount = 5
)

// MigratorOptions configures optional migrator behavior. Zero values in RetryInterval
// and Stage2WorkerCount are replaced with defaults when passed to New.
type MigratorOptions struct {
	RetryInterval     time.Duration
	Stage2WorkerCount int
}

// Migrator performs the migration from LINSTOR CRs to the new control-plane CRs.
type Migrator struct {
	client            kubecl.Client
	dynamicClient     dynamic.Interface
	log               *slog.Logger
	retryInterval     time.Duration
	stage2WorkerCount int
}

// New creates a new Migrator instance.
func New(client kubecl.Client, dynamicClient dynamic.Interface, log *slog.Logger, opts MigratorOptions) *Migrator {
	if opts.RetryInterval <= 0 {
		opts.RetryInterval = DefaultRetryInterval
	}
	if opts.Stage2WorkerCount <= 0 {
		opts.Stage2WorkerCount = DefaultStage2WorkerCount
	}
	return &Migrator{
		client:            client,
		dynamicClient:     dynamicClient,
		log:               log,
		retryInterval:     opts.RetryInterval,
		stage2WorkerCount: opts.Stage2WorkerCount,
	}
}

// Run executes the full migration workflow: pre-flight checks, stage 1 (resource migration),
// stage 2 (adopt exit maintenance), and stage 3 (LINSTOR cleanup).
func (m *Migrator) Run(ctx context.Context) error {
	if err := m.checkNewControlPlaneEnabled(ctx); err != nil {
		return err
	}

	if err := m.crdExists(ctx, config.NewControlPlaneCRDName); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("new control-plane CRD %q not found in cluster; ensure the new control-plane is installed before running migration", config.NewControlPlaneCRDName)
		}
		return fmt.Errorf("failed to check new control-plane CRD %q: %w", config.NewControlPlaneCRDName, err)
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
func (m *Migrator) ensureConfigMap(ctx context.Context) (string, error) {
	cm := &corev1.ConfigMap{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: config.ModuleNamespace,
		Name:      config.MigrationConfigMapName,
	}, cm)

	if err == nil {
		state := cm.Data["state"]
		if state == "" {
			state = config.StateNotStarted
		}
		m.log.Debug("migration ConfigMap exists", "state", state)
		return state, nil
	}

	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("failed to get ConfigMap %s/%s: %w", config.ModuleNamespace, config.MigrationConfigMapName, err)
	}

	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.MigrationConfigMapName,
			Namespace: config.ModuleNamespace,
		},
		Data: map[string]string{
			"state": config.StateNotStarted,
		},
	}
	if err := m.client.Create(ctx, cm); err != nil {
		if apierrors.IsAlreadyExists(err) {
			m.log.Debug("migration ConfigMap was created by another instance")
			return config.StateNotStarted, nil
		}
		return "", fmt.Errorf("failed to create ConfigMap %s/%s: %w", config.ModuleNamespace, config.MigrationConfigMapName, err)
	}

	m.log.Info("created migration ConfigMap", "namespace", config.ModuleNamespace, "name", config.MigrationConfigMapName)
	return config.StateNotStarted, nil
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

// retryTransient runs fn until it succeeds or returns a non-transient error.
func (m *Migrator) retryTransient(ctx context.Context, label string, fn func() error) error {
	for {
		err := fn()
		if err == nil {
			return nil
		}
		if !isTransientAPIError(err) {
			return err
		}
		m.log.Warn("transient error, retrying", "step", label, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.retryInterval):
		}
	}
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

// checkNewControlPlaneEnabled verifies that newControlPlane is set to true in ModuleConfig.
func (m *Migrator) checkNewControlPlaneEnabled(ctx context.Context) error {
	mc := &d8commonapi.ModuleConfig{}
	if err := m.client.Get(ctx, kubecl.ObjectKey{Name: config.ModuleName}, mc); err != nil {
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

// isTransientAPIError reports errors that should not fail stage 2/3 retry loops (network blips, overload, etc.).
func isTransientAPIError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		code := int(statusErr.Status().Code)
		switch code {
		case http.StatusRequestTimeout, http.StatusTooManyRequests,
			http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) {
			return true
		}
		if code == http.StatusConflict {
			return true
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// DNS / connection failures often surface as *net.OpError without Timeout().
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
