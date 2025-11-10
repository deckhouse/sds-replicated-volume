package rv

import (
	"context"
	"fmt"
	"log/slog"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type resourceStatusReconcileRequestHandler struct {
	ctx context.Context
	log *slog.Logger
	cl  client.Client
	rdr client.Reader
	rv  *v1alpha2.ReplicatedVolume
}

func (h *resourceStatusReconcileRequestHandler) Handle() error {
	// collect owned RVRs and LLVs using cache index by owner
	var rvrList v1alpha2.ReplicatedVolumeReplicaList
	if err := h.cl.List(h.ctx, &rvrList, client.MatchingFields{"index.rvOwnerName": h.rv.Name}); err != nil {
		return fmt.Errorf("listing rvrs: %w", err)
	}
	var ownedRvrs []*v1alpha2.ReplicatedVolumeReplica
	for i := range rvrList.Items {
		ownedRvrs = append(ownedRvrs, &rvrList.Items[i])
	}

	var llvList snc.LVMLogicalVolumeList
	if err := h.cl.List(h.ctx, &llvList, client.MatchingFields{"index.rvOwnerName": h.rv.Name}); err != nil {
		return fmt.Errorf("listing llvs: %w", err)
	}
	var ownedLLVs []*snc.LVMLogicalVolume
	for i := range llvList.Items {
		ownedLLVs = append(ownedLLVs, &llvList.Items[i])
	}

	// evaluate readiness
	allReady := true

	for _, rvr := range ownedRvrs {
		cond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha2.ConditionTypeReady)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			allReady = false
			break
		}
	}

	if allReady {
		for _, llv := range ownedLLVs {
			if llv.Status == nil || llv.Status.Phase != "Created" {
				allReady = false
				break
			}
			specQty, err := resource.ParseQuantity(llv.Spec.Size)
			if err != nil {
				return err
			}
			if llv.Status.ActualSize.Cmp(specQty) < 0 {
				allReady = false
				break
			}
		}
	}

	if !allReady {
		return nil
	}

	// set RV Ready=True
	return api.PatchWithConflictRetry(h.ctx, h.cl, h.rv, func(rv *v1alpha2.ReplicatedVolume) error {
		if rv.Status == nil {
			rv.Status = &v1alpha2.ReplicatedVolumeStatus{}
		}
		meta.SetStatusCondition(
			&rv.Status.Conditions,
			metav1.Condition{
				Type:               v1alpha2.ConditionTypeReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: rv.Generation,
				Reason:             "All resources synced",
			},
		)
		return nil
	})
}
