package rv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/deckhouse/sds-common-lib/utils"
	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster/topology"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
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

type replicaInfo struct {
	Node             *corev1.Node
	NodeAddress      corev1.NodeAddress
	Zone             string
	LVG              *snc.LVMVolumeGroup
	LLVProps         cluster.LLVProps
	PublishRequested bool
	Score            *replicaScoreBuilder
}

type replicaScoreBuilder struct {
	disklessPurpose  bool
	withDisk         bool
	publishRequested bool
	alreadyExists    bool
}

func (b *replicaScoreBuilder) clusterHasDiskless() {
	b.disklessPurpose = true
}

func (b *replicaScoreBuilder) replicaWithDisk() {
	b.withDisk = true
}

func (b *replicaScoreBuilder) replicaAlreadyExists() {
	b.alreadyExists = true
}

func (b *replicaScoreBuilder) replicaPublishRequested() {
	b.publishRequested = true
}

func (b *replicaScoreBuilder) Build() []topology.Score {
	baseScore := topology.Score(100)
	maxScore := topology.Score(1000000)
	var scores []topology.Score
	if b.withDisk {
		if b.publishRequested || b.alreadyExists {
			scores = append(scores, maxScore)
		} else {
			scores = append(scores, baseScore)
		}
	} else {
		scores = append(scores, topology.NeverSelect)
	}

	if b.disklessPurpose {
		if b.withDisk {
			scores = append(scores, baseScore)
		} else {
			// prefer nodes without disk for diskless purposes
			scores = append(scores, baseScore*2)
		}
	}
	return scores
}

func (h *resourceReconcileRequestHandler) Handle() error {
	h.log.Info("controller: reconcile resource", "name", h.rv.Name)

	// tie-breaker
	var needTieBreaker bool
	var counts = []int{int(h.rv.Spec.Replicas)}
	if h.rv.Spec.Replicas%2 == 0 {
		needTieBreaker = true
		counts = append(counts, 1)
	}

	zones := make(map[string]struct{}, len(h.rv.Spec.Zones))
	for _, zone := range h.rv.Spec.Zones {
		zones[zone] = struct{}{}
	}

	lvgRefs := make(map[string]*v1alpha2.LVGRef, len(h.rv.Spec.LVM.LVMVolumeGroups))
	for i := range h.rv.Spec.LVM.LVMVolumeGroups {
		lvgRefs[h.rv.Spec.LVM.LVMVolumeGroups[i].Name] = &h.rv.Spec.LVM.LVMVolumeGroups[i]
	}

	pool := map[string]*replicaInfo{}

	nodeList := &corev1.NodeList{}
	if err := h.rdr.List(h.ctx, nodeList); err != nil {
		return fmt.Errorf("getting nodes: %w", err)
	}

	for node := range uslices.Ptrs(nodeList.Items) {
		nodeZone := node.Labels["topology.kubernetes.io/zone"]
		if _, ok := zones[nodeZone]; ok {

			// TODO ignore non-ready nodes?
			addr, found := uiter.Find(
				slices.Values(node.Status.Addresses),
				func(addr corev1.NodeAddress) bool {
					return addr.Type == corev1.NodeInternalIP
				},
			)
			if !found {
				h.log.Warn("ignoring node, because it has no InternalIP address", "node.Name", node.Name)
				continue
			}

			ri := &replicaInfo{
				Node:        node,
				NodeAddress: addr,
				Zone:        nodeZone,
				Score:       &replicaScoreBuilder{},
			}

			if needTieBreaker {
				ri.Score.clusterHasDiskless()
			}

			pool[node.Name] = ri
		}
	}

	// validate:
	// - LVGs are in nodePool
	// - only one LVGs on a node
	// - all publishRequested have LVG
	// TODO: validate LVG status?
	lvgList := &snc.LVMVolumeGroupList{}
	if err := h.rdr.List(h.ctx, lvgList); err != nil {
		return fmt.Errorf("getting lvgs: %w", err)
	}

	publishRequestedFoundLVG := make([]bool, len(h.rv.Spec.PublishRequested))
	for lvg := range uslices.Ptrs(lvgList.Items) {
		lvgRef, ok := lvgRefs[lvg.Name]
		if !ok {
			continue
		}

		if h.rv.Spec.LVM.Type == "Thin" {
			var lvgPoolFound bool
			for _, tp := range lvg.Spec.ThinPools {
				if lvgRef.ThinPoolName == tp.Name {
					lvgPoolFound = true
				}
			}
			if !lvgPoolFound {
				return fmt.Errorf("thin pool '%s' not found in LVG '%s'", lvgRef.ThinPoolName, lvg.Name)
			}
		}

		var publishRequested bool
		for i := range h.rv.Spec.PublishRequested {
			if lvg.Spec.Local.NodeName == h.rv.Spec.PublishRequested[i] {
				publishRequestedFoundLVG[i] = true
				publishRequested = true
			}
		}

		if repl, ok := pool[lvg.Spec.Local.NodeName]; !ok {
			return fmt.Errorf("lvg '%s' is on node '%s', which is not in any of specified zones", lvg.Name, lvg.Spec.Local.NodeName)
		} else if repl.LVG != nil {
			return fmt.Errorf("lvg '%s' is on the same node, as lvg '%s'", lvg.Name, repl.LVG.Name)
		} else {
			switch h.rv.Spec.LVM.Type {
			case "Thin":
				repl.LLVProps = cluster.ThinVolumeProps{
					PoolName: lvgRef.ThinPoolName,
				}
			case "Thick":
				repl.LLVProps = cluster.ThickVolumeProps{
					Contigous: utils.Ptr(true),
				}
			default:
				return fmt.Errorf("unsupported volume Type: '%s' has type '%s'", lvg.Name, h.rv.Spec.LVM.Type)
			}

			repl.LVG = lvg
			repl.Score.replicaWithDisk()
			if publishRequested {
				repl.Score.replicaPublishRequested()
				repl.PublishRequested = true
			}
		}
	}

	for i, found := range publishRequestedFoundLVG {
		if !found {
			return fmt.Errorf("publishRequested can not be satisfied - no LVG found for node '%s'", h.rv.Spec.PublishRequested[i])
		}
	}

	// prioritize existing nodes
	rvrClient := &rvrClientImpl{rdr: h.rdr, log: h.log.WithGroup("rvrClient")}
	rvrs, err := rvrClient.ByReplicatedVolumeName(h.ctx, h.rv.Name)
	if err != nil {
		return fmt.Errorf("getting rvrs: %w", err)
	}
	for i := range rvrs {
		repl := pool[rvrs[i].Spec.NodeName]
		repl.Score.replicaAlreadyExists()
	}

	// solve topology
	var nodeSelector topology.NodeSelector
	switch h.rv.Spec.Topology {
	case "TransZonal":
		sel := topology.NewTransZonalMultiPurposeNodeSelector(len(counts))
		for nodeName, repl := range pool {
			h.log.Info("setting node for selection with TransZonalMultiPurposeNodeSelector", "nodeName", nodeName, "zone", repl.Zone, "scores", repl.Score.Build())
			sel.SetNode(nodeName, repl.Zone, repl.Score.Build())
		}
		nodeSelector = sel
	case "Zonal":
		sel := topology.NewZonalMultiPurposeNodeSelector(len(counts))
		for nodeName, repl := range pool {
			h.log.Info("setting node for selection with ZonalMultiPurposeNodeSelector", "nodeName", nodeName, "zone", repl.Zone, "scores", repl.Score.Build())
			sel.SetNode(nodeName, repl.Zone, repl.Score.Build())
		}
		nodeSelector = sel
	case "Ignore":
		sel := topology.NewMultiPurposeNodeSelector(len(counts))
		for nodeName, repl := range pool {
			h.log.Info("setting node for selection with MultiPurposeNodeSelector", "nodeName", nodeName, "zone", repl.Zone, "scores", repl.Score.Build())
			sel.SetNode(nodeName, repl.Score.Build())
		}
		nodeSelector = sel
	default:
		return fmt.Errorf("unknown topology: %s", h.rv.Spec.Topology)
	}

	h.log.Info("selecting nodes", "counts", counts)
	selectedNodes, err := nodeSelector.SelectNodes(counts)
	if err != nil {
		return fmt.Errorf("selecting nodes: %w", err)
	}
	h.log.Info("selected nodes", "selectedNodes", selectedNodes)

	// Build cluster with required clients and port range (non-cached reader for data fetches)

	lvgByNode := make(map[string]string, len(pool))
	for nodeName, ri := range pool {
		if ri.LVG == nil {
			continue
		}
		lvgByNode[nodeName] = ri.LVG.Name
	}

	clr := cluster.New(
		h.ctx,
		h.log,
		rvrClient,
		&nodeRVRClientImpl{rdr: h.rdr, log: h.log.WithGroup("nodeRvrClient")},
		drbdPortRange{min: uint(h.cfg.DRBDMinPort), max: uint(h.cfg.DRBDMaxPort)},
		&llvClientImpl{
			rdr:       h.rdr,
			log:       h.log.WithGroup("llvClient"),
			lvgByNode: lvgByNode,
		},
		h.rv.Name,
		h.rv.Spec.Size.Value(),
		h.rv.Spec.SharedSecret,
	)

	// diskful
	quorum := h.rv.Spec.Replicas/2 + 1
	qmr := h.rv.Spec.Replicas/2 + 1

	for _, nodeName := range selectedNodes[0] {
		repl := pool[nodeName]

		clr.AddReplica(nodeName, repl.NodeAddress.Address, repl.PublishRequested, quorum, qmr).
			AddVolume(repl.LVG.Name, repl.LVG.Spec.ActualVGNameOnTheNode, repl.LLVProps)
	}

	if needTieBreaker {
		nodeName := selectedNodes[1][0]
		repl := pool[nodeName]
		clr.AddReplica(nodeName, repl.NodeAddress.Address, repl.PublishRequested, quorum, qmr)
	}

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
		err := wait.PollUntilContextTimeout(h.ctx, 500*time.Millisecond, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := h.cl.Get(ctx, client.ObjectKeyFromObject(target), target); client.IgnoreNotFound(err) != nil {
				return false, err
			}
			if target.Status == nil {
				return false, nil
			}
			cond := meta.FindStatusCondition(target.Status.Conditions, v1alpha2.ConditionTypeReady)

			if cond == nil || cond.ObservedGeneration < target.Generation {
				return false, nil
			}

			if cond.Status == metav1.ConditionTrue ||
				(cond.Status == metav1.ConditionFalse && cond.Reason == v1alpha2.ReasonWaitingForInitialSync) {
				return true, nil
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

		if err := api.PatchWithConflictRetry(
			h.ctx,
			h.cl,
			action.ReplicatedVolumeReplica,
			func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
				rvr.SetFinalizers(
					slices.DeleteFunc(
						rvr.Finalizers,
						func(f string) bool { return f == cluster.ControllerFinalizerName },
					),
				)
				return nil
			},
		); err != nil {
			h.log.Error("RVR patch failed (remove finalizer)", "err", err)
			return err
		}

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
			if err := h.cl.Get(ctx, client.ObjectKeyFromObject(target), target); client.IgnoreNotFound(err) != nil {
				return false, err
			}
			if target.Status == nil || target.Status.Phase != "Created" {
				return false, nil
			}
			specQty, err := resource.ParseQuantity(target.Spec.Size)
			if err != nil {
				return false, err
			}
			if target.Status.ActualSize.Cmp(specQty) < 0 {
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

		if err := api.PatchWithConflictRetry(
			h.ctx,
			h.cl,
			action.LVMLogicalVolume,
			func(llv *snc.LVMLogicalVolume) error {
				llv.SetFinalizers(
					slices.DeleteFunc(
						llv.Finalizers,
						func(f string) bool { return f == cluster.ControllerFinalizerName },
					),
				)
				return nil
			},
		); err != nil {
			h.log.Error("LLV patch failed (remove finalizer)", "err", err)
			return err
		}

		if err := h.cl.Delete(h.ctx, action.LVMLogicalVolume); client.IgnoreNotFound(err) != nil {
			h.log.Error("LLV delete failed", "name", action.LVMLogicalVolume.Name, "err", err)
			return err
		}
		h.log.Debug("LLV delete done", "name", action.LVMLogicalVolume.Name)
		return nil
	case cluster.WaitAndTriggerInitialSync:
		h.log.Debug("WaitAndTriggerInitialSync", "name", h.rv.Name)
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
			if err := api.PatchWithConflictRetry(h.ctx, h.cl, h.rv, func(rv *v1alpha2.ReplicatedVolume) error {
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
			}); err != nil {
				h.log.Error("RV patch failed (setting Ready=true)", "name", h.rv.Name, "err", err)
				return err
			}
			h.log.Debug("RV patch done (setting Ready=true)", "name", h.rv.Name)

			h.log.Info("All resources synced")
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
	case cluster.TriggerRVRResize:
		rvr := action.ReplicatedVolumeReplica

		if err := api.PatchWithConflictRetry(h.ctx, h.cl, rvr, func(r *v1alpha2.ReplicatedVolumeReplica) error {
			ann := r.GetAnnotations()
			if ann == nil {
				ann = map[string]string{}
			}
			ann[v1alpha2.AnnotationKeyNeedResize] = "true"
			r.SetAnnotations(ann)
			return nil
		}); err != nil {
			h.log.Error("RVR patch failed (need-resize)", "name", rvr.Name, "err", err)
			return err
		}
		h.log.Debug("RVR patch done (need-resize)", "name", rvr.Name)
		return nil
	default:
		panic("unknown action type")
	}
}
