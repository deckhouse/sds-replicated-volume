package rv

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// drbdPortRange implements cluster.DRBDPortRange backed by controller config
type drbdPortRange struct {
	min uint
	max uint
}

const (
	waitPollInterval = 500 * time.Millisecond
	waitPollTimeout  = 2 * time.Minute
)

func (d drbdPortRange) PortMinMax() (uint, uint) { return d.min, d.max }

type resourceReconcileRequestHandler struct {
	ctx    context.Context
	log    *slog.Logger
	cl     client.Client
	rdr    client.Reader
	scheme *runtime.Scheme
	cfg    *ReconcilerClusterConfig
	rv     *v1alpha2.ReplicatedVolume
}

type replicaInfo struct {
	Node             *corev1.Node
	NodeAddress      corev1.NodeAddress
	Zone             string
	LVG              *snc.LVMVolumeGroup
	PublishRequested bool
	Score            *replicaScoreBuilder
}

func (h *resourceReconcileRequestHandler) Handle() error {
	h.log.Info("controller: reconcile resource", "name", h.rv.Name)

	// Build RV adapter once
	rvAdapter, err := cluster.NewRVAdapter(h.rv)
	if err != nil {
		return err
	}

	// fast path for desired 0 replicas: skip nodes/LVGs/topology, reconcile existing only
	if h.rv.Spec.Replicas == 0 {
		return h.reconcileWithSelection(rvAdapter, nil, nil, nil)
	}

	// tie-breaker and desired counts
	var needTieBreaker bool
	counts := []int{int(h.rv.Spec.Replicas)}
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

	pool, err := h.buildNodePool(zones, needTieBreaker)
	if err != nil {
		return err
	}

	if err := h.applyLVGs(pool, lvgRefs); err != nil {
		return err
	}

	_, err = h.ownedRVRsAndPrioritize(pool)
	if err != nil {
		return err
	}

	// solve topology
	nodeSelector, err := h.buildNodeSelector(pool, len(counts))
	if err != nil {
		return err
	}

	h.log.Info("selecting nodes", "counts", counts)
	selectedNodes, err := nodeSelector.SelectNodes(counts)
	if err != nil {
		return fmt.Errorf("selecting nodes: %w", err)
	}
	h.log.Info("selected nodes", "selectedNodes", selectedNodes)

	var tieNode *string
	if needTieBreaker {
		n := selectedNodes[1][0]
		tieNode = &n
	}
	return h.reconcileWithSelection(rvAdapter, pool, selectedNodes[0], tieNode)
}

func (h *resourceReconcileRequestHandler) processAction(untypedAction any) error {
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
	case cluster.PatchRVR:
		// Patch existing RVR and wait until Ready/SafeForInitialSync
		target := &v1alpha2.ReplicatedVolumeReplica{}
		target.Name = action.RVR.Name()
		h.log.Debug("RVR patch start", "name", target.Name)
		if err := api.PatchWithConflictRetry(h.ctx, h.cl, target, func(r *v1alpha2.ReplicatedVolumeReplica) error {
			changes, err := action.Writer.WriteToRVR(r)
			if err != nil {
				return err
			}
			if len(changes) == 0 {
				h.log.Info("no changes")
			} else {
				h.log.Info("fields changed", "changes", changes.String())
			}
			return nil
		}); err != nil {
			h.log.Error("RVR patch failed", "name", target.Name, "err", err)
			return err
		}
		h.log.Debug("RVR patch done", "name", target.Name)
		h.log.Debug("RVR wait start", "name", target.Name)
		err := wait.PollUntilContextTimeout(h.ctx, waitPollInterval, waitPollTimeout, true, func(ctx context.Context) (bool, error) {
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
	case cluster.CreateRVR:
		// Create new RVR and wait until Ready/SafeForInitialSync
		h.log.Debug("RVR create start")
		target := &v1alpha2.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: fmt.Sprintf("%s-", h.rv.Name),
				Finalizers:   []string{ControllerFinalizerName},
			},
		}
		if err := controllerutil.SetControllerReference(h.rv, target, h.scheme); err != nil {
			return err
		}

		if _, err := action.Writer.WriteToRVR(target); err != nil {
			h.log.Error("RVR init failed", "err", err)
			return err
		}
		if err := h.cl.Create(h.ctx, target); err != nil {
			h.log.Error("RVR create failed", "err", err)
			return err
		}
		h.log.Debug("RVR create done", "name", target.Name)
		h.log.Debug("RVR wait start", "name", target.Name)
		err := wait.PollUntilContextTimeout(h.ctx, waitPollInterval, waitPollTimeout, true, func(ctx context.Context) (bool, error) {
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

		// If waiting for initial sync - trigger and wait for completion

		readyCond := meta.FindStatusCondition(target.Status.Conditions, v1alpha2.ConditionTypeReady)
		if readyCond != nil &&
			readyCond.Status == metav1.ConditionFalse &&
			readyCond.Reason == v1alpha2.ReasonWaitingForInitialSync &&
			action.InitialSyncRequired {
			h.log.Info("Trigger initial sync via primary-force", "name", target.Name)
			if err := api.PatchWithConflictRetry(h.ctx, h.cl, target, func(r *v1alpha2.ReplicatedVolumeReplica) error {
				ann := r.GetAnnotations()
				if ann == nil {
					ann = map[string]string{}
				}
				ann[v1alpha2.AnnotationKeyPrimaryForce] = "true"
				r.SetAnnotations(ann)
				return nil
			}); err != nil {
				h.log.Error("RVR patch failed (primary-force)", "name", target.Name, "err", err)
				return err
			}
			h.log.Info("Primary-force set, waiting for initial sync to complete", "name", target.Name)
			if err := wait.PollUntilContextTimeout(h.ctx, waitPollInterval, waitPollTimeout, true, func(ctx context.Context) (bool, error) {
				if err := h.cl.Get(ctx, client.ObjectKeyFromObject(target), target); client.IgnoreNotFound(err) != nil {
					return false, err
				}
				if target.Status == nil {
					return false, nil
				}
				isCond := meta.FindStatusCondition(target.Status.Conditions, v1alpha2.ConditionTypeInitialSync)
				if isCond == nil || isCond.ObservedGeneration < target.Generation {
					return false, nil
				}
				return isCond.Status == metav1.ConditionTrue, nil
			}); err != nil {
				h.log.Error("RVR wait failed (initial sync)", "name", target.Name, "err", err)
				return err
			}
			h.log.Info("Initial sync completed", "name", target.Name)
		}
		return nil
	case cluster.DeleteRVR:
		h.log.Debug("RVR delete start", "name", action.RVR.Name())
		target := &v1alpha2.ReplicatedVolumeReplica{}
		target.Name = action.RVR.Name()
		if err := api.PatchWithConflictRetry(
			h.ctx,
			h.cl,
			target,
			func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
				rvr.SetFinalizers(
					slices.DeleteFunc(
						rvr.Finalizers,
						func(f string) bool { return f == ControllerFinalizerName },
					),
				)
				return nil
			},
		); err != nil {
			h.log.Error("RVR patch failed (remove finalizer)", "err", err)
			return err
		}

		if err := h.cl.Delete(h.ctx, target); client.IgnoreNotFound(err) != nil {
			h.log.Error("RVR delete failed", "name", target.Name, "err", err)
			return err
		}
		h.log.Debug("RVR delete done", "name", target.Name)
		return nil
	case cluster.PatchLLV:
		target := &snc.LVMLogicalVolume{}
		target.Name = action.LLV.LLVName()
		h.log.Debug("LLV patch start", "name", target.Name)
		if err := api.PatchWithConflictRetry(h.ctx, h.cl, target, func(llv *snc.LVMLogicalVolume) error {
			changes, err := action.Writer.WriteToLLV(llv)
			if err != nil {
				return err
			}
			if len(changes) == 0 {
				h.log.Info("no changes")
			} else {
				h.log.Info("fields changed", "changes", changes.String())
			}
			return nil
		}); err != nil {
			h.log.Error("LLV patch failed", "name", target.Name, "err", err)
			return err
		}
		h.log.Debug("LLV patch done", "name", target.Name)
		h.log.Debug("LLV wait start", "name", target.Name)
		err := wait.PollUntilContextTimeout(h.ctx, waitPollInterval, waitPollTimeout, true, func(ctx context.Context) (bool, error) {
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
	case cluster.CreateLLV:
		// Create new LLV and wait until Created with size satisfied
		h.log.Debug("LLV create start")
		target := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: fmt.Sprintf("%s-", h.rv.Name),
				Finalizers:   []string{ControllerFinalizerName},
			},
		}
		if err := controllerutil.SetControllerReference(h.rv, target, h.scheme); err != nil {
			return err
		}
		if _, err := action.Writer.WriteToLLV(target); err != nil {
			h.log.Error("LLV init failed", "err", err)
			return err
		}
		if err := h.cl.Create(h.ctx, target); err != nil {
			h.log.Error("LLV create failed", "err", err)
			return err
		}
		h.log.Debug("LLV create done", "name", target.Name)
		h.log.Debug("LLV wait start", "name", target.Name)
		err := wait.PollUntilContextTimeout(h.ctx, waitPollInterval, waitPollTimeout, true, func(ctx context.Context) (bool, error) {
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
	case cluster.DeleteLLV:
		h.log.Debug("LLV delete start", "name", action.LLV.LLVName())
		target := &snc.LVMLogicalVolume{}
		target.Name = action.LLV.LLVName()

		if err := api.PatchWithConflictRetry(
			h.ctx,
			h.cl,
			target,
			func(llv *snc.LVMLogicalVolume) error {
				llv.SetFinalizers(
					slices.DeleteFunc(
						llv.Finalizers,
						func(f string) bool { return f == ControllerFinalizerName },
					),
				)
				return nil
			},
		); err != nil {
			h.log.Error("LLV patch failed (remove finalizer)", "err", err)
			return err
		}

		if err := h.cl.Delete(h.ctx, target); client.IgnoreNotFound(err) != nil {
			h.log.Error("LLV delete failed", "name", target.Name, "err", err)
			return err
		}
		h.log.Debug("LLV delete done", "name", target.Name)
		return nil
	// TODO: initial sync/Ready condition handling for RV is not implemented in cluster2 flow yet
	case cluster.ResizeRVR:
		// trigger resize via annotation
		target := &v1alpha2.ReplicatedVolumeReplica{}
		target.Name = action.RVR.Name()
		if err := api.PatchWithConflictRetry(h.ctx, h.cl, target, func(r *v1alpha2.ReplicatedVolumeReplica) error {
			ann := r.GetAnnotations()
			if ann == nil {
				ann = map[string]string{}
			}
			ann[v1alpha2.AnnotationKeyNeedResize] = "true"
			r.SetAnnotations(ann)
			return nil
		}); err != nil {
			h.log.Error("RVR patch failed (need-resize)", "name", target.Name, "err", err)
			return err
		}
		h.log.Debug("RVR patch done (need-resize)", "name", target.Name)
		return nil
	default:
		panic("unknown action type")
	}
}

// buildNodePool lists nodes, filters by zones and prepares replicaInfo pool with scores.
func (h *resourceReconcileRequestHandler) buildNodePool(zones map[string]struct{}, needTieBreaker bool) (map[string]*replicaInfo, error) {
	pool := map[string]*replicaInfo{}
	nodeList := &corev1.NodeList{}
	if err := h.rdr.List(h.ctx, nodeList); err != nil {
		return nil, fmt.Errorf("getting nodes: %w", err)
	}
	for node := range uslices.Ptrs(nodeList.Items) {
		nodeZone := node.Labels["topology.kubernetes.io/zone"]
		if _, ok := zones[nodeZone]; !ok {
			continue
		}
		addr, found := uiter.Find(
			slices.Values(node.Status.Addresses),
			func(addr corev1.NodeAddress) bool { return addr.Type == corev1.NodeInternalIP },
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
			ri.Score.ClusterHasDiskless()
		}
		pool[node.Name] = ri
	}
	return pool, nil
}

// applyLVGs validates LVGs and marks pool entries with LVG selection and extra scoring.
func (h *resourceReconcileRequestHandler) applyLVGs(pool map[string]*replicaInfo, lvgRefs map[string]*v1alpha2.LVGRef) error {
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
		repl, ok := pool[lvg.Spec.Local.NodeName]
		if !ok {
			return fmt.Errorf("lvg '%s' is on node '%s', which is not in any of specified zones", lvg.Name, lvg.Spec.Local.NodeName)
		}
		if repl.LVG != nil {
			return fmt.Errorf("lvg '%s' is on the same node, as lvg '%s'", lvg.Name, repl.LVG.Name)
		}
		repl.LVG = lvg
		repl.Score.NodeWithDisk()
		if publishRequested {
			repl.Score.PublishRequested()
			repl.PublishRequested = true
		}
	}
	for i, found := range publishRequestedFoundLVG {
		if !found {
			return fmt.Errorf("publishRequested can not be satisfied - no LVG found for node '%s'", h.rv.Spec.PublishRequested[i])
		}
	}
	return nil
}

// ownedRVRsAndPrioritize fetches existing RVRs, marks corresponding nodes and returns the list.
func (h *resourceReconcileRequestHandler) ownedRVRsAndPrioritize(pool map[string]*replicaInfo) ([]v1alpha2.ReplicatedVolumeReplica, error) {
	var rvrList v1alpha2.ReplicatedVolumeReplicaList
	if err := h.cl.List(h.ctx, &rvrList, client.MatchingFields{"index.rvOwnerName": h.rv.Name}); err != nil {
		return nil, fmt.Errorf("listing rvrs: %w", err)
	}
	ownedRvrs := rvrList.Items
	for i := range ownedRvrs {
		if repl, ok := pool[ownedRvrs[i].Spec.NodeName]; ok {
			repl.Score.AlreadyExists()
		}
	}
	return ownedRvrs, nil
}

// buildNodeSelector builds a selector according to topology and fills it with nodes/scores.
func (h *resourceReconcileRequestHandler) buildNodeSelector(pool map[string]*replicaInfo, countsLen int) (topology.NodeSelector, error) {
	switch h.rv.Spec.Topology {
	case "TransZonal":
		sel := topology.NewTransZonalMultiPurposeNodeSelector(countsLen)
		for nodeName, repl := range pool {
			h.log.Info("setting node for selection with TransZonalMultiPurposeNodeSelector", "nodeName", nodeName, "zone", repl.Zone, "scores", repl.Score.Build())
			sel.SetNode(nodeName, repl.Zone, repl.Score.Build())
		}
		return sel, nil
	case "Zonal":
		sel := topology.NewZonalMultiPurposeNodeSelector(countsLen)
		for nodeName, repl := range pool {
			h.log.Info("setting node for selection with ZonalMultiPurposeNodeSelector", "nodeName", nodeName, "zone", repl.Zone, "scores", repl.Score.Build())
			sel.SetNode(nodeName, repl.Zone, repl.Score.Build())
		}
		return sel, nil
	case "Ignore":
		sel := topology.NewMultiPurposeNodeSelector(countsLen)
		for nodeName, repl := range pool {
			h.log.Info("setting node for selection with MultiPurposeNodeSelector", "nodeName", nodeName, "zone", repl.Zone, "scores", repl.Score.Build())
			sel.SetNode(nodeName, repl.Score.Build())
		}
		return sel, nil
	default:
		return nil, fmt.Errorf("unknown topology: %s", h.rv.Spec.Topology)
	}
}

// reconcileWithSelection builds cluster from provided selection and reconciles existing/desired state.
// pool may be nil when no nodes are needed (replicas=0). diskfulNames may be empty. tieNodeName is optional.
func (h *resourceReconcileRequestHandler) reconcileWithSelection(
	rvAdapter cluster.RVAdapter,
	pool map[string]*replicaInfo,
	diskfulNames []string,
	tieNodeName *string,
) error {
	var rvNodes []cluster.RVNodeAdapter
	var nodeMgrs []cluster.NodeManager

	// diskful nodes
	for _, nodeName := range diskfulNames {
		repl := pool[nodeName]
		rvNode, err := cluster.NewRVNodeAdapter(rvAdapter, repl.Node, repl.LVG)
		if err != nil {
			return err
		}
		rvNodes = append(rvNodes, rvNode)
		nodeMgrs = append(nodeMgrs, cluster.NewNodeManager(drbdPortRange{min: uint(h.cfg.DRBDMinPort), max: uint(h.cfg.DRBDMaxPort)}, nodeName))
	}
	// optional diskless tie-breaker
	if tieNodeName != nil {
		repl := pool[*tieNodeName]
		rvNode, err := cluster.NewRVNodeAdapter(rvAdapter, repl.Node, nil)
		if err != nil {
			return err
		}
		rvNodes = append(rvNodes, rvNode)
		nodeMgrs = append(nodeMgrs, cluster.NewNodeManager(drbdPortRange{min: uint(h.cfg.DRBDMinPort), max: uint(h.cfg.DRBDMaxPort)}, *tieNodeName))
	}

	// build cluster
	clr, err := cluster.NewCluster(h.log, rvAdapter, rvNodes, nodeMgrs)
	if err != nil {
		return err
	}

	// add existing RVRs/LLVs
	var ownedRvrsList v1alpha2.ReplicatedVolumeReplicaList
	if err := h.cl.List(h.ctx, &ownedRvrsList, client.MatchingFields{"index.rvOwnerName": h.rv.Name}); err != nil {
		return fmt.Errorf("listing rvrs: %w", err)
	}
	ownedRvrs := ownedRvrsList.Items
	for i := range ownedRvrs {
		ra, err := cluster.NewRVRAdapter(&ownedRvrs[i])
		if err != nil {
			return err
		}
		if err := clr.AddExistingRVR(ra); err != nil {
			return err
		}
	}

	var llvList snc.LVMLogicalVolumeList
	if err := h.cl.List(h.ctx, &llvList, client.MatchingFields{"index.rvOwnerName": h.rv.Name}); err != nil {
		return fmt.Errorf("listing llvs: %w", err)
	}
	ownedLLVs := llvList.Items
	for i := range ownedLLVs {
		llv := &ownedLLVs[i]
		la, err := cluster.NewLLVAdapter(llv)
		if err != nil {
			return err
		}
		if err := clr.AddExistingLLV(la); err != nil {
			return err
		}
	}

	// reconcile
	action, err := clr.Reconcile()
	if err != nil {
		return err
	}
	if action != nil {
		if err := h.processAction(action); err != nil {
			return err
		}
	}

	// update ready condition
	return h.updateRVStatus(ownedRvrs, ownedLLVs)
}
func (h *resourceReconcileRequestHandler) updateRVStatus(ownedRvrs []v1alpha2.ReplicatedVolumeReplica, ownedLLVs []snc.LVMLogicalVolume) error {
	allReady := true
	minSizeBytes, sizeFound := h.findMinimalActualSizeBytes(ownedRvrs)
	publishProvided := h.findPublishProvided(ownedRvrs)
	for i := range ownedRvrs {
		rvr := &ownedRvrs[i]
		cond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha2.ConditionTypeReady)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			allReady = false
			break
		}
	}

	// list owned LLVs
	if allReady {
		for i := range ownedLLVs {
			llv := &ownedLLVs[i]
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
		// update ActualSize from minimal DRBD device size, if known
		if sizeFound && minSizeBytes > 0 {
			rv.Status.ActualSize = *resource.NewQuantity(minSizeBytes, resource.BinarySI)
		}
		// update PublishProvided from actual primaries
		rv.Status.PublishProvided = publishProvided
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

// findPublishProvided returns names of nodes that are in DRBD Primary role (max 2 as per CRD).
func (h *resourceReconcileRequestHandler) findPublishProvided(ownedRvrs []v1alpha2.ReplicatedVolumeReplica) []string {
	var publishProvided []string
	for i := range ownedRvrs {
		rvr := &ownedRvrs[i]
		if rvr.Status != nil && rvr.Status.DRBD != nil && rvr.Status.DRBD.Role == "Primary" && rvr.Spec.NodeName != "" {
			publishProvided = append(publishProvided, rvr.Spec.NodeName)
		}
	}
	return publishProvided
}

// findMinimalActualSizeBytes returns the minimal DRBD-reported device size in bytes across replicas.
func (h *resourceReconcileRequestHandler) findMinimalActualSizeBytes(ownedRvrs []v1alpha2.ReplicatedVolumeReplica) (int64, bool) {
	var minSizeBytes int64
	var found bool
	for i := range ownedRvrs {
		rvr := &ownedRvrs[i]
		if rvr.Status == nil || rvr.Status.DRBD == nil || len(rvr.Status.DRBD.Devices) == 0 {
			continue
		}
		sizeKB := int64(rvr.Status.DRBD.Devices[0].Size)
		if sizeKB <= 0 {
			continue
		}
		sizeBytes := sizeKB * 1024
		if !found || sizeBytes < minSizeBytes {
			minSizeBytes = sizeBytes
			found = true
		}
	}
	return minSizeBytes, found
}
