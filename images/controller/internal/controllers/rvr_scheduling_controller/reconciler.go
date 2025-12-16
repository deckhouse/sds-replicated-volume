package rvr_scheduling_controller

import (
	"context"
	"fmt"
	"net/http"
	"os"
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

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
	ext    *schedulerExtenderClient
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	extURL := os.Getenv("SCHEDULER_EXTENDER_URL")
	var ext *schedulerExtenderClient
	if extURL != "" {
		ext = &schedulerExtenderClient{
			httpClient: http.DefaultClient,
			url:        extURL,
		}
	}
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
		ext:    ext,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", req)

	rv, rsc, replicasForRV, res, err := r.prepareSchedulingContext(ctx, req, log)
	if err != nil {
		return reconcile.Result{}, err
	}
	if res != nil {
		return *res, nil
	}

	if err := r.scheduleDiskfulLocalPhase(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.scheduleDiskfulPhase(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.scheduleAccessPhase(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.scheduleTieBreakerPhase(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.updateScheduledConditions(ctx, replicasForRV, log); err != nil {
		log.Error(err, "failed to update Scheduled conditions on replicas")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func isRVReady(status *v1alpha3.ReplicatedVolumeStatus) bool {
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
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil, &reconcile.Result{}, nil
		}
		log.Error(err, "unable to get ReplicatedVolume")
		return nil, nil, nil, nil, err
	}

	if rv.Status == nil {
		return nil, nil, nil, &reconcile.Result{}, nil
	}
	if !isRVReady(rv.Status) {
		return nil, nil, nil, &reconcile.Result{}, nil
	}
	if rv.Spec.ReplicatedStorageClassName == "" {
		return nil, nil, nil, &reconcile.Result{}, nil
	}

	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil, &reconcile.Result{}, nil
		}
		log.Error(err, "unable to get ReplicatedStorageClass")
		return nil, nil, nil, nil, err
	}

	replicaList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, replicaList); err != nil {
		log.Error(err, "unable to list ReplicatedVolumeReplica")
		return nil, nil, nil, nil, err
	}

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
	if rsc.Spec.VolumeAccess != "Local" {
		return nil
	}
	if len(rv.Spec.PublishOn) == 0 {
		return nil
	}

	lvgToNodes, err := r.getLVGToNodesByStoragePool(ctx, rsc, log)
	if err != nil {
		return err
	}

	allowedDiskfulNodes, err := r.filterLVGNodesByCapacity(ctx, rv, lvgToNodes)
	if err != nil {
		return err
	}

	unscheduledDiskfulReplicas, publishNodesWithoutAnyReplica := buildDiskfulLocalCandidates(
		rv,
		replicasForRV,
		allowedDiskfulNodes,
	)

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
	if rsc.Spec.VolumeAccess == "Local" {
		return nil
	}

	unscheduledDiskfulReplicas := getUnscheduledReplicasList(replicasForRV, v1alpha3.ReplicaTypeDiskful)
	if len(unscheduledDiskfulReplicas) == 0 {
		return nil
	}

	rspLvgsToNodesMap, err := r.getLVGToNodesByStoragePool(ctx, rsc, log)
	if err != nil {
		log.Error(err, "failed to determine diskful candidate nodes")
		return err
	}

	capacityFilteredDiskfulNodes, err := r.filterLVGNodesByCapacity(ctx, rv, rspLvgsToNodesMap)
	if err != nil {
		return err
	}

	nodesWithAnyReplica := getNodesWithAnyReplicaMap(replicasForRV)

	rawNodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		return err
	}

	nodeNameToRSCZone, err := filterNodesByRSCTopology(rawNodeNameToZone, rsc)
	if err != nil {
		return err
	}

	allPublishNodes := getPublishOnNodeSet(rv)
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
	// Phase applies only when publishOn is set
	if len(rv.Spec.PublishOn) == 0 {
		return nil
	}

	// Build a set of target nodes from rv.spec.publishOn.
	publishNodeSet := getPublishOnNodeSet(rv)

	// Track nodes that already have any replica of this RV and collect Access replicas without nodeName.
	nodesWithAnyReplica := getNodesWithAnyReplicaMap(replicasForRV)
	unscheduledAccessReplicas := getUnscheduledReplicasList(replicasForRV, "Access")

	if len(unscheduledAccessReplicas) == 0 {
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

type tieBreakerContext struct {
	unscheduled         []*v1alpha3.ReplicatedVolumeReplica
	nodesWithAnyReplica map[string]bool
	nodeNameToRSCZone   map[string]string
}

type tieBreakerAssignment struct {
	tb       *v1alpha3.ReplicatedVolumeReplica
	nodeName string
}

func (r *Reconciler) buildTieBreakerContext(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) (tieBreakerContext, error) {
	ctxData := tieBreakerContext{
		unscheduled:         getUnscheduledReplicasList(replicasForRV, "TieBreaker"),
		nodesWithAnyReplica: getNodesWithAnyReplicaMap(replicasForRV),
	}

	if len(ctxData.unscheduled) == 0 {
		return ctxData, nil
	}

	rawNodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		return tieBreakerContext{}, err
	}

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

	if targetZone == "" {
		for _, zone := range ctxData.nodeNameToRSCZone {
			targetZone = zone
			break
		}
	}

	if targetZone == "" {
		return nil, fmt.Errorf("cannot determine target zone for Zonal topology")
	}

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
	patched := rvr.DeepCopy()
	patched.Spec.NodeName = nodeName

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
	ctxData, err := r.buildTieBreakerContext(ctx, rv, rsc, replicasForRV, log)
	if err != nil {
		return err
	}

	if len(ctxData.unscheduled) == 0 {
		return nil
	}

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

	return r.applyTieBreakerAssignments(ctx, assignments, log)
}

func getPublishOnNodeSet(rv *v1alpha3.ReplicatedVolume) map[string]struct{} {
	publishNodeSet := make(map[string]struct{}, len(rv.Spec.PublishOn))
	for _, nodeName := range rv.Spec.PublishOn {
		publishNodeSet[nodeName] = struct{}{}
	}

	return publishNodeSet
}

func getNodesWithAnyReplicaMap(
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
) map[string]bool {
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

	if r.ext == nil {
		// No external capacity filter configured — allow all nodes that have required LVGs.
		return nodesFromLVGMap(lvgToNodeNamesMap), nil
	}

	return r.ext.filterNodesBySchedulerExtender(ctx, rv, lvgToNodeNamesMap, r.log)
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

	rawNodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		log.Error(err, "unable to list Nodes while applying topology in DiskfulLocal phase")
		return nil, err
	}

	nodeNameToRSCZone, err := filterNodesByRSCTopology(rawNodeNameToZone, rsc)
	if err != nil {
		return nil, err
	}

	var topoFiltered []string
	for _, nodeName := range publishNodesWithoutAnyReplica {
		if _, ok := nodeNameToRSCZone[nodeName]; !ok {
			return nil, fmt.Errorf(
				"cannot schedule Diskful Local replica on publishOn node %s due to topology constraints (topology=%s, zones=%v)",
				nodeName, rsc.Spec.Topology, rsc.Spec.Zones,
			)
		}
		topoFiltered = append(topoFiltered, nodeName)
	}

	if rsc.Spec.Topology == "Zonal" || rsc.Spec.Topology == "TransZonal" {
		zoneHasReplicaMap := make(map[string]bool)

		for _, rvr := range replicasForRV {
			if rvr.Spec.Type != v1alpha3.ReplicaTypeDiskful || rvr.Spec.NodeName == "" {
				continue
			}
			zone, ok := nodeNameToRSCZone[rvr.Spec.NodeName]
			if !ok {
				return nil, fmt.Errorf(
					"existing Diskful replica %s on node %s does not match topology constraints (topology=%s, zones=%v)",
					rvr.Name, rvr.Spec.NodeName, rsc.Spec.Topology, rsc.Spec.Zones,
				)
			}
			if rsc.Spec.Topology == "TransZonal" && zoneHasReplicaMap[zone] {
				return nil, fmt.Errorf(
					"cannot satisfy TransZonal topology: multiple Diskful replicas already exist in zone %s",
					zone,
				)
			}
			zoneHasReplicaMap[zone] = true
		}

		for _, nodeName := range topoFiltered {
			zone := nodeNameToRSCZone[nodeName]
			if rsc.Spec.Topology == "Zonal" {
				if len(zoneHasReplicaMap) > 0 {
					for existingZone := range zoneHasReplicaMap {
						if zone != existingZone {
							return nil, fmt.Errorf(
								"cannot satisfy Zonal topology: publishOn node %s is in zone %s while existing Diskful replicas are in zone %s",
								nodeName, zone, existingZone,
							)
						}
						break
					}
				}
				zoneHasReplicaMap[zone] = true
			}

			if rsc.Spec.Topology == "TransZonal" {
				if zoneHasReplicaMap[zone] {
					return nil, fmt.Errorf(
						"cannot satisfy TransZonal topology: publishOn node %s is in zone %s which already has a Diskful replica",
						nodeName, zone,
					)
				}
				zoneHasReplicaMap[zone] = true
			}
		}
	}

	return topoFiltered, nil
}

func (r *Reconciler) getLVGToNodesByStoragePool(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	log logr.Logger,
) (map[string][]string, error) {
	if rsc.Spec.StoragePool == "" {
		return nil, nil
	}

	rsp := &v1alpha1.ReplicatedStoragePool{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rsc.Spec.StoragePool}, rsp); err != nil {
		// If the storage pool does not exist, do not add extra restrictions.
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		log.Error(err, "unable to get ReplicatedStoragePool", "name", rsc.Spec.StoragePool)
		return nil, err
	}

	if len(rsp.Spec.LVMVolumeGroups) == 0 {
		return nil, nil
	}

	rspLvgNames := make(map[string]struct{}, len(rsp.Spec.LVMVolumeGroups))
	for _, g := range rsp.Spec.LVMVolumeGroups {
		rspLvgNames[g.Name] = struct{}{}
	}

	lvgList := &snc.LVMVolumeGroupList{}
	if err := r.cl.List(ctx, lvgList); err != nil {
		log.Error(err, "unable to list LVMVolumeGroup")
		return nil, err
	}

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
	nodes := &corev1.NodeList{}
	if err := r.cl.List(ctx, nodes); err != nil {
		log.Error(err, "unable to list Nodes")
		return nil, err
	}

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
	result := make(map[string]string, len(nodeNameToZone))
	rscHasZones := len(rsc.Spec.Zones) > 0

	for nodeName, zone := range nodeNameToZone {
		nodeHasZone := zone != ""

		switch rsc.Spec.Topology {
		case "TransZonal", "Zonal":
			if !nodeHasZone {
				return nil, fmt.Errorf("node %s has no zone label %s", nodeName, nodeZoneLabel)
			}
			if rscHasZones && !slices.Contains(rsc.Spec.Zones, zone) {
				continue
			}
		case "Any":
			if rscHasZones {
				if !nodeHasZone || !slices.Contains(rsc.Spec.Zones, zone) {
					continue
				}
			}
		default:
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
	if len(publishNodeSet) == 0 {
		return nil
	}

	var candidateNodes []string
	for nodeName := range publishNodeSet {
		if !nodesWithAnyReplica[nodeName] {
			candidateNodes = append(candidateNodes, nodeName)
		}
	}

	if len(candidateNodes) == 0 {
		return nil
	}

	nodesToFillCount := len(candidateNodes)
	if len(unscheduledDiskfulReplicas) < nodesToFillCount {
		nodesToFillCount = len(unscheduledDiskfulReplicas)
	}

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

		patched := rvr.DeepCopy()
		patched.Spec.NodeName = nodeName

		if err := r.cl.Patch(ctx, patched, client.MergeFrom(rvr)); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			log.Error(err, "Failed to patch Diskful replica NodeName for TransZonal topology")
			return err
		}

		nodesWithAnyReplica[nodeName] = true
		usedZones[zone] = struct{}{}
	}

	return nil
}
