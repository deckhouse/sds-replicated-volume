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

package rv

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1alpha2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2old"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

type resourceDeleteRequestHandler struct {
	ctx context.Context
	log *slog.Logger
	cl  client.Client
	rv  *v1alpha2.ReplicatedVolume
}

func (h *resourceDeleteRequestHandler) Handle() error {
	// 1) Ensure spec.replicas=0 (idempotent)
	var patchedGen int64
	if err := api.PatchWithConflictRetry(h.ctx, h.cl, h.rv, func(rv *v1alpha2.ReplicatedVolume) error {
		// no-op if already 0
		if rv.Spec.Replicas != 0 {
			rv.Spec.Replicas = 0
		}
		return nil
	}); err != nil {
		return fmt.Errorf("set replicas=0: %w", err)
	}

	// Re-fetch to capture new Generation for waiting
	if err := h.cl.Get(h.ctx, client.ObjectKeyFromObject(h.rv), h.rv); err != nil {
		return fmt.Errorf("refetch rv: %w", err)
	}
	patchedGen = h.rv.Generation

	// 2) Wait until Ready=True with ObservedGeneration >= patchedGen
	if err := wait.PollUntilContextTimeout(h.ctx, waitPollInterval, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := h.cl.Get(ctx, client.ObjectKeyFromObject(h.rv), h.rv); err != nil {
			return false, err
		}
		cond := meta.FindStatusCondition(h.rv.Status.Conditions, v1alpha2.ConditionTypeReady)
		if cond == nil {
			return false, nil
		}
		// wait until controller observed this generation
		if cond.ObservedGeneration < patchedGen {
			return false, nil
		}
		return cond.Status == metav1.ConditionTrue, nil
	}); err != nil {
		return fmt.Errorf("waiting for rv ready after replicas=0: %w", err)
	}

	// 3) Remove finalizer to complete deletion
	if err := api.PatchWithConflictRetry(h.ctx, h.cl, h.rv, func(rv *v1alpha2.ReplicatedVolume) error {
		var out []string
		for _, f := range rv.Finalizers {
			if f != ControllerFinalizerName {
				out = append(out, f)
			}
		}
		rv.Finalizers = out
		return nil
	}); err != nil {
		return fmt.Errorf("remove finalizer: %w", err)
	}

	//
	{
		var rvrList v1alpha2.ReplicatedVolumeReplicaList
		if err := h.cl.List(h.ctx, &rvrList, client.MatchingFields{"index.rvOwnerName": h.rv.Name}); err != nil {
			return fmt.Errorf("listing rvrs: %w", err)
		}

		for i := range rvrList.Items {
			rvr := &rvrList.Items[i]
			err := api.PatchWithConflictRetry(
				h.ctx, h.cl, rvr,
				func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
					var out []string
					for _, f := range rvr.Finalizers {
						if f != ControllerFinalizerName {
							out = append(out, f)
						}
					}
					rvr.Finalizers = out
					return nil
				},
			)
			if err != nil {
				return fmt.Errorf("removing finalizer: %w", err)
			}
		}
	}

	{
		var llvList snc.LVMLogicalVolumeList
		if err := h.cl.List(h.ctx, &llvList, client.MatchingFields{"index.rvOwnerName": h.rv.Name}); err != nil {
			return fmt.Errorf("listing llvs: %w", err)
		}

		for i := range llvList.Items {
			llv := &llvList.Items[i]
			err := api.PatchWithConflictRetry(
				h.ctx, h.cl, llv,
				func(rvr *snc.LVMLogicalVolume) error {
					var out []string
					for _, f := range rvr.Finalizers {
						if f != ControllerFinalizerName {
							out = append(out, f)
						}
					}
					rvr.Finalizers = out
					return nil
				},
			)
			if err != nil {
				return fmt.Errorf("removing finalizer: %w", err)
			}
		}
	}
	return nil
}
