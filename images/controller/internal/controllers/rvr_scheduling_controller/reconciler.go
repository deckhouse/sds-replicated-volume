package rvr_scheduling_controller

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

const nodeZoneLabel = "topology.kubernetes.io/zone"

type tieBreakerContext struct {
	unscheduled         []*v1alpha3.ReplicatedVolumeReplica
	nodesWithAnyReplica map[string]bool
	nodeNameToRSCZone   map[string]string
}

type tieBreakerAssignment struct {
	tb       *v1alpha3.ReplicatedVolumeReplica
	nodeName string
}

type Reconciler struct {
	cl             client.Client
	log            logr.Logger
	scheme         *runtime.Scheme
	extenderClient *SchedulerExtenderClient
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	// Initialize reconciler with Kubernetes client, logger, scheme and scheduler-extender client.
	return &Reconciler{
		cl:             cl,
		log:            log,
		scheme:         scheme,
		extenderClient: NewSchedulerHTTPClient(),
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", req)

	// Load ReplicatedVolume, its ReplicatedStorageClass and all relevant replicas.
	// The helper may also return an early reconcile.Result (e.g. when RV is not ready yet).
	rv, rsc, replicasForRV, res, err := r.prepareSchedulingContext(ctx, req, log)
	if err != nil {
		return reconcile.Result{}, err
	}
	if res != nil {
		return *res, nil
	}

	// Phase 1: place Diskful replicas for Local access mode (respecting publishOn and topology).
	if err := r.scheduleDiskfulLocalPhase(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	// Phase 2: place remaining Diskful replicas for non-Local access modes (respecting topology and capacity).
	if err := r.scheduleDiskfulPhase(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	// Phase 3: place Access replicas on publishOn nodes that still lack any replica.
	if err := r.scheduleAccessPhase(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	// Phase 4: place TieBreaker replicas according to topology and existing placements.
	if err := r.scheduleTieBreakerPhase(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	// Finally, update Scheduled conditions on all replicas based on their NodeName.
	if err := r.updateScheduledConditions(ctx, replicasForRV, log); err != nil {
		log.Error(err, "failed to update Scheduled conditions on replicas")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func isRVReady(status *v1alpha3.ReplicatedVolumeStatus) bool {
	// ReplicatedVolume is considered ready only when status is present and ConditionReady is true.
	if status == nil {
		return false
	}

	return meta.IsStatusConditionTrue(status.Conditions, v1alpha3.ConditionTypeReady)
}

func (r *Reconciler) prepareSchedulingContext(
	ctx context.Context,
	req reconcile.Request,
	log logr.Logger,
) (*v1alpha3.ReplicatedVolume, *v1alpha1.ReplicatedStorageClass, []v1alpha3.ReplicatedVolumeReplica, *reconcile.Result, error) {
	// Fetch the target ReplicatedVolume for this reconcile request.
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		// If the volume no longer exists, exit reconciliation without error.
		if apierrors.IsNotFound(err) {
			return nil, nil, nil, &reconcile.Result{}, nil
		}
		log.Error(err, "unable to get ReplicatedVolume")
		return nil, nil, nil, nil, err
	}

	// Do not schedule until status is present, RV is Ready and has a storage class reference.
	if rv.Status == nil {
		return nil, nil, nil, &reconcile.Result{}, nil
	}
	if !isRVReady(rv.Status) {
		return nil, nil, nil, &reconcile.Result{}, nil
	}
	if rv.Spec.ReplicatedStorageClassName == "" {
		return nil, nil, nil, &reconcile.Result{}, nil
	}

	// Load the referenced ReplicatedStorageClass.
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		if apierrors.IsNotFound(err) {
			// If the class is missing, just requeue later.
			return nil, nil, nil, &reconcile.Result{}, nil
		}
		log.Error(err, "unable to get ReplicatedStorageClass")
		return nil, nil, nil, nil, err
	}

	// List all ReplicatedVolumeReplica resources in the cluster.
	replicaList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, replicaList); err != nil {
		log.Error(err, "unable to list ReplicatedVolumeReplica")
		return nil, nil, nil, nil, err
	}

	// Keep only replicas that belong to this RV and are not being deleted.
	var replicasForRV []v1alpha3.ReplicatedVolumeReplica
	for _, rvr := range replicaList.Items {
		if rvr.Spec.ReplicatedVolumeName != rv.Name || !rvr.DeletionTimestamp.IsZero() {
			continue
		}
		replicasForRV = append(replicasForRV, rvr)
	}

	return rv, rsc, replicasForRV, nil, nil
}

func (r *Reconciler) scheduleDiskfulLocalPhase(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) error {
	// This phase is only relevant when VolumeAccess is Local.
	if rsc.Spec.VolumeAccess != "Local" {
		return nil
	}
	// Without publishOn targets there is nothing to schedule in this phase.
	if len(rv.Spec.PublishOn) == 0 {
		return nil
	}

	// Discover nodes that have LVGs belonging to the storage pool referenced by the RSC.
	lvgToNodeNamesMap, err := r.getLVGToNodesByStoragePool(ctx, rsc, log)
	if err != nil {
		return err
	}

	// Optionally filter LVG-backed nodes by capacity using the scheduler-extender.
	allowedDiskfulNodes, err := r.filterLVGNodesByCapacity(ctx, rv, lvgToNodeNamesMap)
	if err != nil {
		return err
	}

	// Build a list of free Diskful replicas and publishOn nodes that currently have no replicas.
	unscheduledDiskfulReplicas, publishNodesWithoutAnyReplica := buildDiskfulLocalCandidates(
		rv,
		replicasForRV,
		allowedDiskfulNodes,
	)

	// Apply topology constraints (Any/Zonal/TransZonal) to the publishOn nodes without replicas.
	nodesToSchedule, err := r.applyDiskfulLocalTopology(
		ctx,
		rv,
		rsc,
		replicasForRV,
		publishNodesWithoutAnyReplica,
		log,
	)
	if err != nil {
		return err
	}

	// Nothing to do if all publishOn nodes already have at least one replica.
	if len(nodesToSchedule) == 0 {
		return nil
	}

	// If there are not enough free Diskful replicas to cover all such nodes, fail scheduling for this phase.
	if len(unscheduledDiskfulReplicas) < len(nodesToSchedule) {
		return fmt.Errorf("not enough Diskful replicas to cover publishOn nodes: have %d, need %d", len(unscheduledDiskfulReplicas), len(nodesToSchedule))
	}

	// Assign free Diskful replicas to publishOn nodes that do not have any replica yet.
	for i, nodeName := range nodesToSchedule {
		rvr := unscheduledDiskfulReplicas[i]
		if err := r.patchReplicaWithNodeName(ctx, rvr, nodeName, log, "Failed to patch replica"); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reconciler) scheduleDiskfulPhase(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) error {
	// Non-Local Diskful scheduling is skipped when access mode is Local.
	if rsc.Spec.VolumeAccess == "Local" {
		return nil
	}

	// Collect all Diskful replicas that are not bound to any node.
	unscheduledDiskfulReplicas := getUnscheduledReplicasList(replicasForRV, v1alpha3.ReplicaTypeDiskful)
	if len(unscheduledDiskfulReplicas) == 0 {
		return nil
	}

	// Discover LVG-backed nodes for the storage pool defined in the RSC.
	rspLvgsToNodesMap, err := r.getLVGToNodesByStoragePool(ctx, rsc, log)
	if err != nil {
		log.Error(err, "failed to determine diskful candidate nodes")
		return err
	}

	// Optionally narrow down LVG nodes by capacity via scheduler-extender.
	capacityFilteredDiskfulNodes, err := r.filterLVGNodesByCapacity(ctx, rv, rspLvgsToNodesMap)
	if err != nil {
		return err
	}

	// Track nodes that already host any replica of this RV.
	nodesWithAnyReplica := getNodesWithAnyReplicaMap(replicasForRV)

	// Build a map of node -> zone for all cluster nodes.
	rawNodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		return err
	}

	// Filter nodes according to RSC topology type and allowed zones.
	nodeNameToRSCZone, err := filterNodesByRSCTopology(rawNodeNameToZone, rsc)
	if err != nil {
		return err
	}

	// Start from all publishOn nodes.
	allPublishNodes := getPublishOnNodeSet(rv)
	// Intersect publishOn nodes with capacity-allowed LVG nodes if extender filtering was applied.
	capacityFilteredPublishNodes := allPublishNodes
	if capacityFilteredDiskfulNodes != nil {
		filtered := make(map[string]struct{}, len(allPublishNodes))
		for nodeName := range allPublishNodes {
			if _, ok := capacityFilteredDiskfulNodes[nodeName]; ok {
				filtered[nodeName] = struct{}{}
			}
		}
		capacityFilteredPublishNodes = filtered
	}

	switch rsc.Spec.Topology {
	case "Any":
		return r.scheduleDiskfulAnyTopology(ctx, unscheduledDiskfulReplicas, nodesWithAnyReplica, capacityFilteredPublishNodes, log)
	case "Zonal":
		return r.scheduleDiskfulZonalTopology(ctx, unscheduledDiskfulReplicas, replicasForRV, nodesWithAnyReplica, capacityFilteredPublishNodes, nodeNameToRSCZone, log)
	case "TransZonal":
		return r.scheduleDiskfulTransZonalTopology(ctx, unscheduledDiskfulReplicas, replicasForRV, nodesWithAnyReplica, capacityFilteredPublishNodes, nodeNameToRSCZone, log)
	default:
		return nil
	}
}

func (r *Reconciler) scheduleAccessPhase(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) error {
	// This phase is only relevant when publishOn is set; otherwise there is no explicit Access placement.
	if len(rv.Spec.PublishOn) == 0 {
		return nil
	}

	// Build a set of target nodes from rv.spec.publishOn.
	publishNodeSet := getPublishOnNodeSet(rv)

	// Track nodes that already have any replica of this RV and collect Access replicas without nodeName.
	nodesWithAnyReplica := getNodesWithAnyReplicaMap(replicasForRV)
	unscheduledAccessReplicas := getUnscheduledReplicasList(replicasForRV, "Access")

	if len(unscheduledAccessReplicas) == 0 {
		// All Access replicas are already scheduled; nothing to do.
		return nil
	}

	// Prefer nodes from publishOn that do not yet have any replica of this RV.
	var candidateNodes []string
	for nodeName := range publishNodeSet {
		if !nodesWithAnyReplica[nodeName] {
			candidateNodes = append(candidateNodes, nodeName)
		}
	}

	if len(candidateNodes) == 0 {
		return nil
	}

	// We are not required to place all Access replicas or to cover all publishOn nodes.
	nodesToFill := len(candidateNodes)
	if len(unscheduledAccessReplicas) < nodesToFill {
		nodesToFill = len(unscheduledAccessReplicas)
	}

	for i := 0; i < nodesToFill; i++ {
		nodeName := candidateNodes[i]
		rvr := unscheduledAccessReplicas[i]

		if err := r.patchReplicaWithNodeName(ctx, rvr, nodeName, log, "Failed to patch Access replica"); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reconciler) buildTieBreakerContext(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) (tieBreakerContext, error) {
	// Start with unscheduled TieBreaker replicas and a map of nodes that already host any replica of this RV.
	ctxData := tieBreakerContext{
		unscheduled:         getUnscheduledReplicasList(replicasForRV, "TieBreaker"),
		nodesWithAnyReplica: getNodesWithAnyReplicaMap(replicasForRV),
	}

	// If there are no unscheduled TieBreaker replicas, there is nothing to plan.
	if len(ctxData.unscheduled) == 0 {
		return ctxData, nil
	}

	// Build node -> zone map from cluster Nodes.
	rawNodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		return tieBreakerContext{}, err
	}

	// Apply RSC topology to filter nodes and attach topology-aware zones to the context.
	nodeNameToRSCZone, err := filterNodesByRSCTopology(rawNodeNameToZone, rsc)
	if err != nil {
		return tieBreakerContext{}, err
	}

	ctxData.nodeNameToRSCZone = nodeNameToRSCZone

	return ctxData, nil
}

func (r *Reconciler) planTieBreakerTransZonal(
	ctxData tieBreakerContext,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
) ([]tieBreakerAssignment, error) {
	// Count how many replicas (any type) already exist in each zone.
	zoneReplicaCount := make(map[string]int)
	for _, rvr := range replicasForRV {
		if rvr.Spec.NodeName == "" {
			continue
		}
		zone, ok := ctxData.nodeNameToRSCZone[rvr.Spec.NodeName]
		if !ok {
			continue
		}
		zoneReplicaCount[zone]++
	}

	assignments := make([]tieBreakerAssignment, 0, len(ctxData.unscheduled))

	// For each unscheduled TieBreaker, place it in a zone with the minimal replica count,
	// choosing a free node in that zone.
	for _, tb := range ctxData.unscheduled {
		minCount := -1
		var candidateZones []string
		for _, zone := range ctxData.nodeNameToRSCZone {
			count := zoneReplicaCount[zone]
			if minCount == -1 || count < minCount {
				minCount = count
				candidateZones = []string{zone}
			} else if count == minCount {
				candidateZones = append(candidateZones, zone)
			}
		}

		var chosenNode, chosenZone string
		for _, zone := range candidateZones {
			for nodeName, z := range ctxData.nodeNameToRSCZone {
				if z != zone {
					continue
				}
				if ctxData.nodesWithAnyReplica[nodeName] {
					continue
				}
				chosenNode = nodeName
				chosenZone = zone
				break
			}
			if chosenNode != "" {
				break
			}
		}

		if chosenNode == "" {
			return nil, fmt.Errorf("cannot schedule TieBreaker: no free node in zones with minimal replica count")
		}

		assignments = append(assignments, tieBreakerAssignment{
			tb:       tb,
			nodeName: chosenNode,
		})

		ctxData.nodesWithAnyReplica[chosenNode] = true
		zoneReplicaCount[chosenZone]++
	}

	return assignments, nil
}

func (r *Reconciler) planTieBreakerZonal(
	ctxData tieBreakerContext,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
) ([]tieBreakerAssignment, error) {
	// Determine the single target zone from already placed replicas; all must be in the same zone.
	var targetZone string
	for _, rvr := range replicasForRV {
		if rvr.Spec.NodeName == "" {
			continue
		}
		zone, ok := ctxData.nodeNameToRSCZone[rvr.Spec.NodeName]
		if !ok {
			continue
		}
		if targetZone == "" {
			targetZone = zone
		} else if targetZone != zone {
			return nil, fmt.Errorf("cannot satisfy Zonal topology: replicas already exist in multiple zones (%s, %s)", targetZone, zone)
		}
	}

	// If there is no existing replica, pick any zone allowed by the topology.
	if targetZone == "" {
		for _, zone := range ctxData.nodeNameToRSCZone {
			targetZone = zone
			break
		}
	}

	if targetZone == "" {
		return nil, fmt.Errorf("cannot determine target zone for Zonal topology")
	}

	// Collect free nodes in the chosen zone.
	var zonalCandidates []string
	for nodeName, zone := range ctxData.nodeNameToRSCZone {
		if zone != targetZone {
			continue
		}
		if ctxData.nodesWithAnyReplica[nodeName] {
			continue
		}
		zonalCandidates = append(zonalCandidates, nodeName)
	}

	limit := len(zonalCandidates)
	if len(ctxData.unscheduled) < limit {
		limit = len(ctxData.unscheduled)
	}

	// Plan assignments for as many unscheduled TieBreaker replicas as we have candidate nodes.
	assignments := make([]tieBreakerAssignment, 0, limit)
	for i := 0; i < limit; i++ {
		nodeName := zonalCandidates[i]
		tb := ctxData.unscheduled[i]

		assignments = append(assignments, tieBreakerAssignment{
			tb:       tb,
			nodeName: nodeName,
		})

		ctxData.nodesWithAnyReplica[nodeName] = true
	}

	return assignments, nil
}

func (r *Reconciler) planTieBreakerAny(
	ctxData tieBreakerContext,
) ([]tieBreakerAssignment, error) {
	// Collect all nodes that passed topology filter and do not yet host any replica of this RV.
	candidateNodes := make([]string, 0, len(ctxData.nodeNameToRSCZone))
	for nodeName := range ctxData.nodeNameToRSCZone {
		if !ctxData.nodesWithAnyReplica[nodeName] {
			candidateNodes = append(candidateNodes, nodeName)
		}
	}

	limit := len(candidateNodes)
	if len(ctxData.unscheduled) < limit {
		limit = len(ctxData.unscheduled)
	}

	// Assign unscheduled TieBreaker replicas to free candidate nodes in arbitrary order.
	assignments := make([]tieBreakerAssignment, 0, limit)
	for i := 0; i < limit; i++ {
		nodeName := candidateNodes[i]
		tb := ctxData.unscheduled[i]

		assignments = append(assignments, tieBreakerAssignment{
			tb:       tb,
			nodeName: nodeName,
		})

		ctxData.nodesWithAnyReplica[nodeName] = true
	}

	return assignments, nil
}

func (r *Reconciler) patchReplicaWithNodeName(
	ctx context.Context,
	rvr *v1alpha3.ReplicatedVolumeReplica,
	nodeName string,
	log logr.Logger,
	errorMessage string,
) error {
	// Prepare a patch with the desired NodeName.
	patched := rvr.DeepCopy()
	patched.Spec.NodeName = nodeName

	// Apply the patch; ignore NotFound errors because the replica may have been deleted meanwhile.
	if err := r.cl.Patch(ctx, patched, client.MergeFrom(rvr)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, errorMessage)
		return err
	}

	return nil
}

func (r *Reconciler) applyTieBreakerAssignments(
	ctx context.Context,
	assignments []tieBreakerAssignment,
	log logr.Logger,
) error {
	// Apply all planned TieBreaker assignments by patching NodeName on each replica.
	for _, a := range assignments {
		if err := r.patchReplicaWithNodeName(ctx, a.tb, a.nodeName, log, "Failed to patch TieBreaker replica"); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reconciler) scheduleTieBreakerPhase(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) error {
	// Build common context for TieBreaker scheduling (unscheduled replicas, topology-filtered nodes, existing placements).
	ctxData, err := r.buildTieBreakerContext(ctx, rv, rsc, replicasForRV, log)
	if err != nil {
		return err
	}

	if len(ctxData.unscheduled) == 0 {
		return nil
	}

	// Choose a planning strategy based on topology type.
	var assignments []tieBreakerAssignment
	switch rsc.Spec.Topology {
	case "TransZonal":
		assignments, err = r.planTieBreakerTransZonal(ctxData, replicasForRV)
	case "Zonal":
		assignments, err = r.planTieBreakerZonal(ctxData, replicasForRV)
	default:
		assignments, err = r.planTieBreakerAny(ctxData)
	}

	if err != nil {
		return err
	}

	// Apply all planned assignments to the cluster.
	return r.applyTieBreakerAssignments(ctx, assignments, log)
}

func getPublishOnNodeSet(rv *v1alpha3.ReplicatedVolume) map[string]struct{} {
	// Convert publishOn slice to a set for fast membership checks.
	publishNodeSet := make(map[string]struct{}, len(rv.Spec.PublishOn))
	for _, nodeName := range rv.Spec.PublishOn {
		publishNodeSet[nodeName] = struct{}{}
	}

	return publishNodeSet
}

func getNodesWithAnyReplicaMap(
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
) map[string]bool {
	// Build a set of nodes that already host at least one replica of this RV.
	nodesWithAnyReplica := make(map[string]bool)

	for _, rvr := range replicasForRV {
		if rvr.Spec.NodeName != "" {
			nodesWithAnyReplica[rvr.Spec.NodeName] = true
		}
	}

	return nodesWithAnyReplica
}

func getUnscheduledReplicasList(
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	replicaType string,
) []*v1alpha3.ReplicatedVolumeReplica {
	// Collect replicas of the given type that do not have a NodeName assigned yet.
	var unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica

	for _, rvr := range replicasForRV {
		if rvr.Spec.Type != replicaType {
			continue
		}
		if rvr.Spec.NodeName == "" {
			unscheduledReplicas = append(unscheduledReplicas, &rvr)
		}
	}

	return unscheduledReplicas
}

func (r *Reconciler) updateScheduledConditions(
	ctx context.Context,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) error {
	for i := range replicasForRV {
		rvr := &replicasForRV[i]

		patch := client.MergeFrom(rvr.DeepCopy())

		if rvr.Status == nil {
			rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}

		status := metav1.ConditionFalse
		reason := v1alpha3.ReasonWaitingForAnotherReplica
		message := ""

		if rvr.Spec.NodeName != "" {
			status = metav1.ConditionTrue
			reason = v1alpha3.ReasonReplicaScheduled
		}

		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			metav1.Condition{
				Type:               v1alpha3.ConditionTypeScheduled,
				Status:             status,
				Reason:             reason,
				Message:            message,
				ObservedGeneration: rvr.Generation,
			},
		)

		if err := r.cl.Status().Patch(ctx, rvr, patch); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			log.Error(err, "failed to patch Scheduled condition on ReplicatedVolumeReplica", "rvr", rvr.Name)
			return err
		}
	}

	return nil
}

func nodesWithoutAnyReplica(
	publishNodeSet map[string]struct{},
	nodesWithAnyReplica map[string]bool,
) []string {
	var publishNodesWithoutAnyReplica []string

	for nodeName := range publishNodeSet {
		if !nodesWithAnyReplica[nodeName] {
			publishNodesWithoutAnyReplica = append(publishNodesWithoutAnyReplica, nodeName)
		}
	}

	return publishNodesWithoutAnyReplica
}

func nodesFromLVGMap(
	lvgToNodes map[string][]string,
) map[string]struct{} {
	nodesWithLVG := make(map[string]struct{})
	for _, nodes := range lvgToNodes {
		for _, n := range nodes {
			nodesWithLVG[n] = struct{}{}
		}
	}
	return nodesWithLVG
}

func (r *Reconciler) filterLVGNodesByCapacity(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	lvgToNodeNamesMap map[string][]string,
) (map[string]struct{}, error) {
	if lvgToNodeNamesMap == nil {
		// No LVG restriction — all publishOn nodes are allowed from capacity perspective.
		return nil, nil
	}

	if len(lvgToNodeNamesMap) == 0 {
		// No LVG-backed nodes found; treat as "no additional restriction".
		return nil, nil
	}

	if r.extenderClient == nil {
		// No external capacity filter configured — allow all nodes that have required LVGs.
		return nodesFromLVGMap(lvgToNodeNamesMap), nil
	}

	return r.extenderClient.filterNodesBySchedulerExtender(ctx, rv, lvgToNodeNamesMap, r.log)
}

func buildDiskfulLocalCandidates(
	rv *v1alpha3.ReplicatedVolume,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	allowedDiskfulNodes map[string]struct{},
) ([]*v1alpha3.ReplicatedVolumeReplica, []string) {
	publishNodeSet := getPublishOnNodeSet(rv)
	if allowedDiskfulNodes != nil {
		filtered := make(map[string]struct{}, len(publishNodeSet))
		for nodeName := range publishNodeSet {
			if _, ok := allowedDiskfulNodes[nodeName]; ok {
				filtered[nodeName] = struct{}{}
			}
		}
		publishNodeSet = filtered
	}

	nodesWithAnyReplica := getNodesWithAnyReplicaMap(replicasForRV)
	unscheduledDiskfulReplicas := getUnscheduledReplicasList(replicasForRV, v1alpha3.ReplicaTypeDiskful)
	publishNodesWithoutAnyReplica := nodesWithoutAnyReplica(publishNodeSet, nodesWithAnyReplica)

	return unscheduledDiskfulReplicas, publishNodesWithoutAnyReplica
}

func (r *Reconciler) applyDiskfulLocalTopology(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	publishNodesWithoutAnyReplica []string,
	log logr.Logger,
) ([]string, error) {
	if len(publishNodesWithoutAnyReplica) == 0 || rsc.Spec.Topology == "" {
		return publishNodesWithoutAnyReplica, nil
	}

	nodesFilteredByTopology, nodeNameToRSCZone, err := r.filterDiskfulLocalPublishNodesByTopology(
		ctx,
		rsc,
		publishNodesWithoutAnyReplica,
		log,
	)
	if err != nil {
		return nil, err
	}

	if rsc.Spec.Topology == "Zonal" || rsc.Spec.Topology == "TransZonal" {
		if err := validateDiskfulLocalZonesForZonalAndTransZonal(
			rsc,
			replicasForRV,
			nodesFilteredByTopology,
			nodeNameToRSCZone,
		); err != nil {
			return nil, err
		}
	}

	return nodesFilteredByTopology, nil
}

func (r *Reconciler) filterDiskfulLocalPublishNodesByTopology(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	publishNodesWithoutAnyReplica []string,
	log logr.Logger,
) ([]string, map[string]string, error) {
	rawNodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		log.Error(err, "unable to list Nodes while applying topology in DiskfulLocal phase")
		return nil, nil, err
	}

	nodeNameToRSCZone, err := filterNodesByRSCTopology(rawNodeNameToZone, rsc)
	if err != nil {
		return nil, nil, err
	}

	var topoFiltered []string
	for _, nodeName := range publishNodesWithoutAnyReplica {
		if _, ok := nodeNameToRSCZone[nodeName]; !ok {
			return nil, nil, fmt.Errorf(
				"cannot schedule Diskful Local replica on publishOn node %s due to topology constraints (topology=%s, zones=%v)",
				nodeName, rsc.Spec.Topology, rsc.Spec.Zones,
			)
		}
		topoFiltered = append(topoFiltered, nodeName)
	}

	return topoFiltered, nodeNameToRSCZone, nil
}

func validateDiskfulLocalZonesForZonalAndTransZonal(
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	topoFiltered []string,
	nodeNameToRSCZone map[string]string,
) error {
	// Track which zones already contain a Diskful replica of this RV.
	zoneHasReplicaMap := make(map[string]bool)

	// First, validate existing Diskful replicas against topology constraints.
	for _, rvr := range replicasForRV {
		if rvr.Spec.Type != v1alpha3.ReplicaTypeDiskful || rvr.Spec.NodeName == "" {
			continue
		}
		zone, ok := nodeNameToRSCZone[rvr.Spec.NodeName]
		if !ok {
			return fmt.Errorf(
				"existing Diskful replica %s on node %s does not match topology constraints (topology=%s, zones=%v)",
				rvr.Name, rvr.Spec.NodeName, rsc.Spec.Topology, rsc.Spec.Zones,
			)
		}
		// For TransZonal we must not have more than one Diskful replica per zone.
		if rsc.Spec.Topology == "TransZonal" && zoneHasReplicaMap[zone] {
			return fmt.Errorf(
				"cannot satisfy TransZonal topology: multiple Diskful replicas already exist in zone %s",
				zone,
			)
		}
		zoneHasReplicaMap[zone] = true
	}

	// Then, validate candidate publishOn nodes that passed the basic topology filter.
	for _, nodeName := range topoFiltered {
		zone := nodeNameToRSCZone[nodeName]
		switch rsc.Spec.Topology {
		case "Zonal":
			// For Zonal, all Diskful replicas (existing and new) must be in the same zone.
			if len(zoneHasReplicaMap) > 0 {
				for existingZone := range zoneHasReplicaMap {
					if zone != existingZone {
						return fmt.Errorf(
							"cannot satisfy Zonal topology: publishOn node %s is in zone %s while existing Diskful replicas are in zone %s",
							nodeName, zone, existingZone,
						)
					}
					break
				}
			}
			zoneHasReplicaMap[zone] = true
		case "TransZonal":
			// For TransZonal, new replicas must go only to zones that do not yet contain any Diskful replica.
			if zoneHasReplicaMap[zone] {
				return fmt.Errorf(
					"cannot satisfy TransZonal topology: publishOn node %s is in zone %s which already has a Diskful replica",
					nodeName, zone,
				)
			}
			zoneHasReplicaMap[zone] = true
		}
	}

	return nil
}

func (r *Reconciler) getLVGToNodesByStoragePool(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	log logr.Logger,
) (map[string][]string, error) {
	// If no storage pool is specified, do not restrict nodes by LVG.
	if rsc.Spec.StoragePool == "" {
		return nil, nil
	}

	// Load the referenced ReplicatedStoragePool.
	rsp := &v1alpha1.ReplicatedStoragePool{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rsc.Spec.StoragePool}, rsp); err != nil {
		// If the storage pool does not exist, do not add extra restrictions.
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		log.Error(err, "unable to get ReplicatedStoragePool", "name", rsc.Spec.StoragePool)
		return nil, err
	}

	// If the pool does not define any LVGs, there is no LVG-based restriction.
	if len(rsp.Spec.LVMVolumeGroups) == 0 {
		return nil, nil
	}

	// Build a set of LVG names that belong to this storage pool.
	rspLvgNames := make(map[string]struct{}, len(rsp.Spec.LVMVolumeGroups))
	for _, g := range rsp.Spec.LVMVolumeGroups {
		rspLvgNames[g.Name] = struct{}{}
	}

	// List all LVMVolumeGroup objects managed by node-configurator.
	lvgList := &snc.LVMVolumeGroupList{}
	if err := r.cl.List(ctx, lvgList); err != nil {
		log.Error(err, "unable to list LVMVolumeGroup")
		return nil, err
	}

	// Build a map LVG name -> slice of node names that report this LVG.
	lvgToNodeNamesMap := make(map[string][]string)
	for _, lvg := range lvgList.Items {
		if _, ok := rspLvgNames[lvg.Name]; !ok {
			continue
		}

		for _, n := range lvg.Status.Nodes {
			lvgToNodeNamesMap[lvg.Name] = append(lvgToNodeNamesMap[lvg.Name], n.Name)
		}
	}

	if len(lvgToNodeNamesMap) == 0 {
		// No LVG-backed nodes found; fall back to "no restriction".
		return nil, nil
	}

	return lvgToNodeNamesMap, nil
}

func (r *Reconciler) getNodeNameToZoneMap(
	ctx context.Context,
	log logr.Logger,
) (map[string]string, error) {
	// List all Kubernetes Nodes to inspect their zone labels.
	nodes := &corev1.NodeList{}
	if err := r.cl.List(ctx, nodes); err != nil {
		log.Error(err, "unable to list Nodes")
		return nil, err
	}

	// Build a map from node name to its zone (may be empty if label is missing).
	nodeNameToZone := make(map[string]string, len(nodes.Items))

	for _, node := range nodes.Items {
		zone := node.Labels[nodeZoneLabel]
		nodeNameToZone[node.Name] = zone
	}

	return nodeNameToZone, nil
}

func filterNodesByRSCTopology(
	nodeNameToZone map[string]string,
	rsc *v1alpha1.ReplicatedStorageClass,
) (map[string]string, error) {
	// Result collects nodes that satisfy the RSC topology and zone constraints.
	result := make(map[string]string, len(nodeNameToZone))
	rscHasZones := len(rsc.Spec.Zones) > 0

	for nodeName, zone := range nodeNameToZone {
		nodeHasZone := zone != ""

		switch rsc.Spec.Topology {
		case "TransZonal", "Zonal":
			// For Zonal/TransZonal, every node must have a zone label and (optionally) be in one of RSC zones.
			if !nodeHasZone {
				return nil, fmt.Errorf("node %s has no zone label %s", nodeName, nodeZoneLabel)
			}
			if rscHasZones && !slices.Contains(rsc.Spec.Zones, zone) {
				continue
			}
		case "Any":
			// For Any, we only filter by zones if RSC explicitly lists them.
			if rscHasZones {
				if !nodeHasZone || !slices.Contains(rsc.Spec.Zones, zone) {
					continue
				}
			}
		default:
			// Unknown topology type is considered configuration error.
			return nil, fmt.Errorf("rsc %s has unknown topology type %s", rsc.Name, rsc.Spec.Topology)
		}

		result[nodeName] = zone
	}

	return result, nil
}

func (r *Reconciler) scheduleDiskfulAnyTopology(
	ctx context.Context,
	unscheduledDiskfulReplicas []*v1alpha3.ReplicatedVolumeReplica,
	nodesWithAnyReplica map[string]bool,
	publishNodeSet map[string]struct{},
	log logr.Logger,
) error {
	// If there are no publishOn nodes, nothing to schedule in "Any" topology.
	if len(publishNodeSet) == 0 {
		return nil
	}

	// Select publishOn nodes that do not yet host any replica of this RV.
	var candidateNodes []string
	for nodeName := range publishNodeSet {
		if !nodesWithAnyReplica[nodeName] {
			candidateNodes = append(candidateNodes, nodeName)
		}
	}

	if len(candidateNodes) == 0 {
		return nil
	}

	// We can schedule at most min(len(candidates), len(unscheduled replicas)).
	nodesToFillCount := len(candidateNodes)
	if len(unscheduledDiskfulReplicas) < nodesToFillCount {
		nodesToFillCount = len(unscheduledDiskfulReplicas)
	}

	// Assign free Diskful replicas to candidate nodes in arbitrary order.
	for i := 0; i < nodesToFillCount; i++ {
		nodeName := candidateNodes[i]
		rvr := unscheduledDiskfulReplicas[i]

		if err := r.patchReplicaWithNodeName(ctx, rvr, nodeName, log, "Failed to patch Diskful replica for Any topology"); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reconciler) scheduleDiskfulZonalTopology(
	ctx context.Context,
	unscheduledDiskfulReplicas []*v1alpha3.ReplicatedVolumeReplica,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	nodesWithAnyReplica map[string]bool,
	publishNodeSet map[string]struct{},
	nodeNameToRSCZone map[string]string,
	log logr.Logger,
) error {
	// Collect all zones where this RV already has Diskful replicas.
	var zonesWithDiskfulReplicas []string
	for _, rvr := range replicasForRV {
		if rvr.Spec.Type != v1alpha3.ReplicaTypeDiskful {
			continue
		}
		if rvr.Spec.NodeName == "" {
			continue
		}
		zone, ok := nodeNameToRSCZone[rvr.Spec.NodeName]
		if !ok {
			continue
		}
		if !slices.Contains(zonesWithDiskfulReplicas, zone) {
			zonesWithDiskfulReplicas = append(zonesWithDiskfulReplicas, zone)
		}
	}

	// Determine the single target zone:
	// - if there are existing Diskful replicas, reuse their zone;
	// - otherwise pick a zone from any publishOn node that passed topology filtering.
	var targetZone string
	if len(zonesWithDiskfulReplicas) > 0 {
		targetZone = zonesWithDiskfulReplicas[0]
	} else {
		for nodeName := range publishNodeSet {
			if zone, ok := nodeNameToRSCZone[nodeName]; ok {
				targetZone = zone
				break
			}
		}
		if targetZone == "" {
			return nil
		}
	}

	// From publishOn, keep only nodes:
	// - that do not yet host any replica of this RV;
	// - that are located in the target zone.
	var candidateNodes []string
	for nodeName := range publishNodeSet {
		if nodesWithAnyReplica[nodeName] {
			continue
		}
		zone, ok := nodeNameToRSCZone[nodeName]
		if !ok {
			continue
		}
		if zone == targetZone {
			candidateNodes = append(candidateNodes, nodeName)
		}
	}

	if len(candidateNodes) == 0 {
		return nil
	}

	// Limit the number of assignments by the number of available unscheduled Diskful replicas.
	nodesToScheduleCount := len(candidateNodes)
	if len(unscheduledDiskfulReplicas) < nodesToScheduleCount {
		nodesToScheduleCount = len(unscheduledDiskfulReplicas)
	}

	// Assign free Diskful replicas to the selected nodes in the target zone.
	for i := 0; i < nodesToScheduleCount; i++ {
		nodeName := candidateNodes[i]
		rvr := unscheduledDiskfulReplicas[i]

		patched := rvr.DeepCopy()
		patched.Spec.NodeName = nodeName

		if err := r.cl.Patch(ctx, patched, client.MergeFrom(rvr)); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			log.Error(err, "Failed to patch Diskful replica NodeName for Zonal topology")
			return err
		}
	}

	return nil
}

func (r *Reconciler) scheduleDiskfulTransZonalTopology(
	ctx context.Context,
	unscheduledDiskfulReplicas []*v1alpha3.ReplicatedVolumeReplica,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	nodesWithAnyReplica map[string]bool,
	publishNodeSet map[string]struct{},
	nodeNameToZone map[string]string,
	log logr.Logger,
) error {
	// Collect zones that already contain a Diskful replica of this RV.
	usedZones := make(map[string]struct{})

	for _, rvr := range replicasForRV {
		if rvr.Spec.Type != v1alpha3.ReplicaTypeDiskful {
			continue
		}
		if rvr.Spec.NodeName == "" {
			continue
		}
		zone, ok := nodeNameToZone[rvr.Spec.NodeName]
		if !ok {
			continue
		}
		usedZones[zone] = struct{}{}
	}

	// For each unscheduled Diskful replica, find a publishOn node in a zone
	// that does not yet contain any Diskful replica of this RV.
	for _, rvr := range unscheduledDiskfulReplicas {
		var candidatesInNewZones []string
		for nodeName := range publishNodeSet {
			if nodesWithAnyReplica[nodeName] {
				continue
			}
			zone, ok := nodeNameToZone[nodeName]
			if !ok {
				continue
			}
			if _, alreadyUsed := usedZones[zone]; alreadyUsed {
				continue
			}
			candidatesInNewZones = append(candidatesInNewZones, nodeName)
		}

		// If there are no candidate nodes in yet-unoccupied zones, stop planning:
		// we don't move existing replicas, and we don't oversubscribe zones.
		if len(candidatesInNewZones) == 0 {
			return nil
		}

		// Pick the first suitable node in a new zone and assign the replica there.
		nodeName := candidatesInNewZones[0]
		zone := nodeNameToZone[nodeName]
		if err := r.patchReplicaWithNodeName(ctx, rvr, nodeName, log, "Failed to patch Diskful replica for TransZonal topology"); err != nil {
			return err
		}

		nodesWithAnyReplica[nodeName] = true
		usedZones[zone] = struct{}{}
	}

	return nil
}
