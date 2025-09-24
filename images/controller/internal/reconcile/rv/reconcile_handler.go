package rv

import (
	"context"
	"errors"
	"log/slog"
	"time"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// client impls moved to separate files

// drbdPortRange implements cluster.DRBDPortRange backed by controller config
type drbdPortRange struct {
	min uint
	max uint
}

func (d drbdPortRange) PortMinMax() (uint, uint) { return d.min, d.max }

type resourceReconcileRequestHandler struct {
	ctx context.Context
	log *slog.Logger
	cl  client.Client
	rdr client.Reader
	cfg *ReconcilerClusterConfig
	rv  *v1alpha2.ReplicatedVolume
}

func (h *resourceReconcileRequestHandler) Handle() error {
	h.log.Info("controller: reconcile resource", "name", h.rv.Name)

	// Build cluster with required clients and port range (non-cached reader for data fetches)
	clr := cluster.New(
		h.ctx,
		&rvrClientImpl{rdr: h.rdr, log: h.log.WithGroup("rvrClient")},
		&nodeRVRClientImpl{rdr: h.rdr, log: h.log.WithGroup("nodeRvrClient")},
		drbdPortRange{min: uint(h.cfg.DRBDMinPort), max: uint(h.cfg.DRBDMaxPort)},
		&llvClientImpl{rdr: h.rdr, log: h.log.WithGroup("llvClient")},
		h.rv.Name,
		"shared-secret", // TODO: source from a Secret/config when available
	)

	clr.AddReplica("a-stefurishin-worker-0", "10.10.11.52", true, 0, 0).AddVolume("vg-1")
	clr.AddReplica("a-stefurishin-worker-1", "10.10.11.149", false, 0, 0).AddVolume("vg-1")
	clr.AddReplica("a-stefurishin-worker-2", "10.10.11.150", false, 0, 0) // diskless

	action, err := clr.Reconcile()
	if err != nil {
		return err
	}

	return h.processAction(action)
}

func (h *resourceReconcileRequestHandler) processAction(untypedAction cluster.Action) error {
	switch action := untypedAction.(type) {
	case cluster.Actions:
		// Execute subactions sequentially using recursion. Stop on first error.
		for _, a := range action {
			if err := h.processAction(a); err != nil {
				return err
			}
		}
		return nil
	case cluster.ParallelActions:
		// Execute in parallel; collect errors
		var eg errgroup.Group
		for _, sa := range action {
			eg.Go(func() error { return h.processAction(sa) })
		}
		return eg.Wait()
	case cluster.RVRPatch:
		h.log.Debug("RVR patch start", "name", action.ReplicatedVolumeReplica.Name)
		if err := api.PatchWithConflictRetry(h.ctx, h.cl, action.ReplicatedVolumeReplica, func(r *v1alpha2.ReplicatedVolumeReplica) error {
			return action.Apply(r)
		}); err != nil {
			h.log.Error("RVR patch failed", "name", action.ReplicatedVolumeReplica.Name, "err", err)
			return err
		}
		h.log.Debug("RVR patch done", "name", action.ReplicatedVolumeReplica.Name)
		return nil
	case cluster.LLVPatch:
		h.log.Debug("LLV patch start", "name", action.LVMLogicalVolume.Name)
		if err := api.PatchWithConflictRetry(h.ctx, h.cl, action.LVMLogicalVolume, func(llv *snc.LVMLogicalVolume) error {
			return action.Apply(llv)
		}); err != nil {
			h.log.Error("LLV patch failed", "name", action.LVMLogicalVolume.Name, "err", err)
			return err
		}
		h.log.Debug("LLV patch done", "name", action.LVMLogicalVolume.Name)
		return nil
	case cluster.CreateReplicatedVolumeReplica:
		h.log.Debug("RVR create start")
		if err := h.cl.Create(h.ctx, action.ReplicatedVolumeReplica); err != nil {
			h.log.Error("RVR create failed", "err", err)
			return err
		}
		h.log.Debug("RVR create done", "name", action.ReplicatedVolumeReplica.Name)
		return nil
	case cluster.WaitReplicatedVolumeReplica:
		// Wait for Ready=True with observedGeneration >= generation
		target := action.ReplicatedVolumeReplica
		h.log.Debug("RVR wait start", "name", target.Name)
		gen := target.GetGeneration()
		err := wait.PollUntilContextTimeout(h.ctx, 500*time.Millisecond, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := h.cl.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
				return false, err
			}
			if target.Status == nil {
				return false, nil
			}
			cond := meta.FindStatusCondition(target.Status.Conditions, v1alpha2.ConditionTypeReady)
			if cond == nil || cond.Status != metav1.ConditionTrue || cond.ObservedGeneration < gen {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			h.log.Error("RVR wait failed", "name", target.Name, "err", err)
			return err
		}
		h.log.Debug("RVR wait done", "name", target.Name)
		return nil
	case cluster.DeleteReplicatedVolumeReplica:
		h.log.Debug("RVR delete start", "name", action.ReplicatedVolumeReplica.Name)
		if err := h.cl.Delete(h.ctx, action.ReplicatedVolumeReplica); client.IgnoreNotFound(err) != nil {
			h.log.Error("RVR delete failed", "name", action.ReplicatedVolumeReplica.Name, "err", err)
			return err
		}
		h.log.Debug("RVR delete done", "name", action.ReplicatedVolumeReplica.Name)
		return nil
	case cluster.CreateLVMLogicalVolume:
		h.log.Debug("LLV create start")
		if err := h.cl.Create(h.ctx, action.LVMLogicalVolume); err != nil {
			h.log.Error("LLV create failed", "err", err)
			return err
		}
		h.log.Debug("LLV create done", "name", action.LVMLogicalVolume.Name)
		return nil
	case cluster.WaitLVMLogicalVolume:
		target := action.LVMLogicalVolume
		h.log.Debug("LLV wait start", "name", target.Name)
		err := wait.PollUntilContextTimeout(h.ctx, 500*time.Millisecond, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := h.cl.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
				return false, err
			}
			if target.Status == nil || target.Status.Phase != "Ready" {
				return false, nil
			}
			specQty, err := resource.ParseQuantity(target.Spec.Size)
			if err != nil {
				return false, err
			}
			if target.Status.ActualSize.Cmp(specQty) != 0 {
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			h.log.Error("LLV wait failed", "name", target.Name, "err", err)
			return err
		}
		h.log.Debug("LLV wait done", "name", target.Name)
		return nil
	case cluster.DeleteLVMLogicalVolume:
		h.log.Debug("LLV delete start", "name", action.LVMLogicalVolume.Name)
		if err := h.cl.Delete(h.ctx, action.LVMLogicalVolume); client.IgnoreNotFound(err) != nil {
			h.log.Error("LLV delete failed", "name", action.LVMLogicalVolume.Name, "err", err)
			return err
		}
		h.log.Debug("LLV delete done", "name", action.LVMLogicalVolume.Name)
		return nil
	case cluster.WaitAndTriggerInitialSync:
		allSynced := true
		allSafeToBeSynced := true
		for _, rvr := range action.ReplicatedVolumeReplicas {
			cond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha2.ConditionTypeInitialSync)
			if cond.Status != metav1.ConditionTrue {
				allSynced = false
			} else if cond.Status != metav1.ConditionFalse || cond.Reason != v1alpha2.ReasonSafeForInitialSync {
				allSafeToBeSynced = false
			}
		}
		if allSynced {
			h.log.Debug("All resources synced")
			return nil
		}
		if !allSafeToBeSynced {
			return errors.New("waiting for resources to become safe for initial sync")
		}

		rvr := action.ReplicatedVolumeReplicas[0]
		h.log.Debug("RVR patch start (primary-force)", "name", rvr.Name)

		if err := api.PatchWithConflictRetry(h.ctx, h.cl, rvr, func(r *v1alpha2.ReplicatedVolumeReplica) error {
			ann := r.GetAnnotations()
			if ann == nil {
				ann = map[string]string{}
			}
			ann[v1alpha2.AnnotationKeyPrimaryForce] = "true"
			r.SetAnnotations(ann)
			return nil
		}); err != nil {
			h.log.Error("RVR patch failed (primary-force)", "name", rvr.Name, "err", err)
			return err
		}
		h.log.Debug("RVR patch done (primary-force)", "name", rvr.Name)
		return nil
	default:
		panic("unknown action type")
	}
}
