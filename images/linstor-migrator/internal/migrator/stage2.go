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
	"net"
	"net/http"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"

	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

const (
	// formationPlanAdopt matches images/controller/internal/controllers/rv_controller/reconciler_formation.go
	formationPlanAdopt = "adopt/v1"
)

// runStage2 polls ReplicatedVolumes that still have the adopt annotation until adopt/v1 formation
// reaches the Exit maintenance step, then clears DRBDResource maintenance and removes adopt annotations.
func (m *Migrator) runStage2(ctx context.Context) error {
	m.log.Info("stage 2: starting adopt exit-maintenance polling",
		"pollInterval", m.stage2PollInterval, "workerCount", m.stage2WorkerCount)
	for {
		err := m.updateMigrationState(ctx, config.StateStage2Started)
		if err == nil {
			break
		}
		if !isTransientAPIError(err) {
			return fmt.Errorf("failed to update migration state: %w", err)
		}
		m.log.Warn("transient error updating migration state to stage2_started, retrying", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.stage2PollInterval):
		}
	}

	remainingRVNames, err := m.listAdoptReplicatedVolumeNames(ctx)
	if err != nil {
		return err
	}
	if len(remainingRVNames) == 0 {
		m.log.Info("stage 2: no ReplicatedVolumes with adopt annotation, marking migration completed")
		return m.updateMigrationStateAllCompleted(ctx)
	}

	m.log.Info("stage 2: tracking ReplicatedVolumes until adopt exit-maintenance", "count", len(remainingRVNames))

	ticker := time.NewTicker(m.stage2PollInterval)
	defer ticker.Stop()

	firstRound := true
	for len(remainingRVNames) > 0 {
		if !firstRound {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		firstRound = false

		// Snapshot keys from remainingRVNames to avoid race conditions.
		// Workers concurrently delete from the map (see processStage2RV), so we
		// cannot iterate over it directly. Copying keys under a short-lived lock
		// ensures we have a stable view of items to process in this iteration.
		var mu sync.Mutex
		names := make([]string, 0, len(remainingRVNames))
		mu.Lock()
		for n := range remainingRVNames {
			names = append(names, n)
		}
		mu.Unlock()

		if len(names) == 0 {
			break
		}

		// Fill the jobs channel outside the critical section. Sending to a
		// channel may block if the buffer is full; doing this without holding
		// the mutex prevents deadlock with workers that need the same mutex
		// to delete finished items from remainingRVNames.
		jobs := make(chan string, len(names))
		for _, n := range names {
			jobs <- n
		}
		close(jobs)

		var wg sync.WaitGroup
		var fatalErr error
		var fatalMu sync.Mutex

		for w := 0; w < m.stage2WorkerCount; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for rvName := range jobs {
					done, perr := m.processStage2RV(ctx, rvName)
					if perr != nil {
						if isTransientAPIError(perr) {
							m.log.Warn("transient API error during stage 2, will retry on next tick",
								"replicatedVolume", rvName, "err", perr)
							continue
						}
						fatalMu.Lock()
						if fatalErr == nil {
							fatalErr = fmt.Errorf("replicatedVolume %q: %w", rvName, perr)
						}
						fatalMu.Unlock()
						return
					}
					if done {
						mu.Lock()
						delete(remainingRVNames, rvName)
						left := len(remainingRVNames)
						mu.Unlock()
						m.log.Info("stage 2: finished ReplicatedVolume", "replicatedVolume", rvName, "remaining", left)
					}
				}
			}()
		}
		wg.Wait()

		if fatalErr != nil {
			return fatalErr
		}
	}

	return m.updateMigrationStateAllCompleted(ctx)
}

func (m *Migrator) updateMigrationStateAllCompleted(ctx context.Context) error {
	for {
		err := m.updateMigrationState(ctx, config.StateAllCompleted)
		if err == nil {
			m.log.Info("stage 2: completed, migration finished")
			return nil
		}
		if !isTransientAPIError(err) {
			return fmt.Errorf("failed to update migration state to all_completed: %w", err)
		}
		m.log.Warn("transient error updating migration state to all_completed, retrying", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.stage2PollInterval):
		}
	}
}

// listAdoptReplicatedVolumeNames returns a set of ReplicatedVolume names (map keys are metadata.name of each RV
// that still has the adopt-RVR annotation). Values are empty structs and are not used.
func (m *Migrator) listAdoptReplicatedVolumeNames(ctx context.Context) (map[string]struct{}, error) {
	for {
		var list srvv1alpha1.ReplicatedVolumeList
		if err := m.client.List(ctx, &list); err != nil {
			if isTransientAPIError(err) {
				m.log.Warn("transient error listing ReplicatedVolumes, retrying", "err", err)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(m.stage2PollInterval):
				}
				continue
			}
			return nil, fmt.Errorf("list ReplicatedVolumes: %w", err)
		}
		out := make(map[string]struct{})
		for i := range list.Items {
			rv := &list.Items[i]
			if rv.Annotations == nil {
				continue
			}
			if _, ok := rv.Annotations[srvv1alpha1.AdoptRVRAnnotationKey]; ok {
				out[rv.Name] = struct{}{}
			}
		}
		return out, nil
	}
}

// processStage2RV returns done=true when the name should be removed from tracking (processed or gone).
func (m *Migrator) processStage2RV(ctx context.Context, rvName string) (done bool, err error) {
	rv := &srvv1alpha1.ReplicatedVolume{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: rvName}, rv); err != nil {
		if apierrors.IsNotFound(err) {
			m.log.Warn("ReplicatedVolume not found during stage 2, dropping from tracking", "replicatedVolume", rvName)
			return true, nil
		}
		return false, err
	}

	if !matchesExitGate(rv) {
		return false, nil
	}

	var drbdrList srvv1alpha1.DRBDResourceList
	if err := m.client.List(ctx, &drbdrList, kubecl.MatchingLabels(map[string]string{
		srvv1alpha1.ReplicatedVolumeLabelKey: rvName,
	})); err != nil {
		return false, err
	}

	for i := range drbdrList.Items {
		drbdr := &drbdrList.Items[i]
		if err := m.clearDRBDRMaintenanceIfSet(ctx, drbdr); err != nil {
			return false, err
		}
	}

	// Re-fetch RV so patches apply to a fresh object.
	if err := m.client.Get(ctx, types.NamespacedName{Name: rvName}, rv); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}

	if err := m.patchRVRemoveAdoptAnnotations(ctx, rv); err != nil {
		return false, err
	}
	return true, nil
}

// matchesExitGate reports whether the ReplicatedVolume is ready to exit DRBD maintenance mode.
// It checks that the adopt formation plan has completed all three required steps.
func matchesExitGate(rv *srvv1alpha1.ReplicatedVolume) bool {
	t := findFormationAdoptTransition(rv)
	if t == nil {
		return false
	}
	if t.PlanID != formationPlanAdopt {
		return false
	}
	if len(t.Steps) != 3 {
		return false
	}
	s0 := t.Steps[0].Status
	s1 := t.Steps[1].Status
	s2 := t.Steps[2].Status
	return s0 == srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted &&
		s1 == srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted &&
		s2 == srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
}

func findFormationAdoptTransition(rv *srvv1alpha1.ReplicatedVolume) *srvv1alpha1.ReplicatedVolumeDatameshTransition {
	for i := range rv.Status.DatameshTransitions {
		t := &rv.Status.DatameshTransitions[i]
		if t.Type == srvv1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation && t.PlanID == formationPlanAdopt {
			return t
		}
	}
	return nil
}

func (m *Migrator) clearDRBDRMaintenanceIfSet(ctx context.Context, drbdr *srvv1alpha1.DRBDResource) error {
	if drbdr.Spec.Maintenance == "" {
		return nil
	}
	old := drbdr.DeepCopy()
	drbdr.Spec.Maintenance = ""
	if err := m.client.Patch(ctx, drbdr, kubecl.MergeFrom(old)); err != nil {
		return err
	}
	m.log.Debug("cleared DRBDResource maintenance", "drbdResource", drbdr.Name)
	return nil
}

func (m *Migrator) patchRVRemoveAdoptAnnotations(ctx context.Context, rv *srvv1alpha1.ReplicatedVolume) error {
	if rv.Annotations == nil {
		return nil
	}
	_, hasAdopt := rv.Annotations[srvv1alpha1.AdoptRVRAnnotationKey]
	_, hasSecret := rv.Annotations[srvv1alpha1.AdoptSharedSecretAnnotationKey]
	if !hasAdopt && !hasSecret {
		return nil
	}
	old := rv.DeepCopy()
	if hasAdopt {
		delete(rv.Annotations, srvv1alpha1.AdoptRVRAnnotationKey)
	}
	if hasSecret {
		delete(rv.Annotations, srvv1alpha1.AdoptSharedSecretAnnotationKey)
	}
	if len(rv.Annotations) == 0 {
		rv.Annotations = nil
	}
	if err := m.client.Patch(ctx, rv, kubecl.MergeFrom(old)); err != nil {
		return err
	}
	m.log.Info("removed adopt annotations from ReplicatedVolume", "replicatedVolume", rv.Name)
	return nil
}

// isTransientAPIError reports errors that should not fail the stage 2 loop (network blips, overload, etc.).
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
