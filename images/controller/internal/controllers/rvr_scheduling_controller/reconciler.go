package rvr_scheduling_controller

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

const nodeZoneLabel = "topology.kubernetes.io/zone"

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{cl: cl, log: log, scheme: scheme}
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
	// Phase applies only to volumes with Local access and when publishOn is explicitly set.
	if rsc.Spec.VolumeAccess != "Local" {
		return nil
	}
	if len(rv.Spec.PublishOn) == 0 {
		return nil
	}

	// Build a set of nodes from rv.spec.publishOn that we want to cover with Diskful replicas.
	publishNodeSet := getPublishNodeSet(rv)

	// Track nodes that already have any replica of this RV and collect Diskful replicas without nodeName.
	nodesWithAnyReplica := getNodesWithAnyReplicaMap(replicasForRV)
	unscheduledDiskfulReplicas := getUnscheduledReplicasList(replicasForRV, v1alpha3.ReplicaTypeDiskful)

	// Determine publishOn nodes that still do not have any replica of this RV.
	publishNodesWithoutAnyReplica := nodesWithoutAnyReplica(publishNodeSet, nodesWithAnyReplica)

	// Nothing to do if all publishOn nodes already have at least one replica.
	if len(publishNodesWithoutAnyReplica) == 0 {
		return nil
	}

	// If there are not enough free Diskful replicas to cover all such nodes, fail scheduling for this phase.
	if len(unscheduledDiskfulReplicas) < len(publishNodesWithoutAnyReplica) {
		return fmt.Errorf("not enough Diskful replicas to cover publishOn nodes: have %d, need %d", len(unscheduledDiskfulReplicas), len(publishNodesWithoutAnyReplica))
	}

	// Assign free Diskful replicas to publishOn nodes that do not have any replica yet.
	for i, nodeName := range publishNodesWithoutAnyReplica {
		rvr := unscheduledDiskfulReplicas[i]

		patched := rvr.DeepCopy()
		patched.Spec.NodeName = nodeName

		if err := r.cl.Patch(ctx, patched, client.MergeFrom(rvr)); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			log.Error(err, "Failed to patch replica NodeName")
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

	nodesWithAnyReplica := getNodesWithAnyReplicaMap(replicasForRV)

	rawNodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		return err
	}

	nodeNameToZone, err := filterNodesByTopology(rawNodeNameToZone, rsc)
	if err != nil {
		return err
	}

	publishNodeSet := getPublishNodeSet(rv)

	switch rsc.Spec.Topology {
	case "Any":
		return r.scheduleDiskfulAnyTopology(ctx, unscheduledDiskfulReplicas, nodesWithAnyReplica, publishNodeSet, log)
	case "Zonal":
		return r.scheduleDiskfulZonalTopology(ctx, unscheduledDiskfulReplicas, replicasForRV, nodesWithAnyReplica, publishNodeSet, nodeNameToZone, log)
	case "TransZonal":
		return r.scheduleDiskfulTransZonalTopology(ctx, unscheduledDiskfulReplicas, replicasForRV, nodesWithAnyReplica, publishNodeSet, nodeNameToZone, log)
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
	publishNodeSet := getPublishNodeSet(rv)

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

		patched := rvr.DeepCopy()
		patched.Spec.NodeName = nodeName

		if err := r.cl.Patch(ctx, patched, client.MergeFrom(rvr)); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			log.Error(err, "Failed to patch Access replica NodeName")
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
	// Placeholder for TieBreaker scheduling phase.
	// Will implement zone-aware placement based on tie-breaker count controller.
	return nil
}

func getPublishNodeSet(rv *v1alpha3.ReplicatedVolume) map[string]struct{} {
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

func filterNodesByTopology(
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

		patched := rvr.DeepCopy()
		patched.Spec.NodeName = nodeName

		if err := r.cl.Patch(ctx, patched, client.MergeFrom(rvr)); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			log.Error(err, "Failed to patch Diskful replica NodeName for Any topology")
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
	nodeNameToZone map[string]string,
	log logr.Logger,
) error {
	var existingZones []string
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
		if !slices.Contains(existingZones, zone) {
			existingZones = append(existingZones, zone)
		}
	}

	var targetZone string
	if len(existingZones) > 0 {
		targetZone = existingZones[0]
	} else {
		for nodeName := range publishNodeSet {
			if zone, ok := nodeNameToZone[nodeName]; ok {
				targetZone = zone
				break
			}
		}
		if targetZone == "" {
			return nil
		}
	}

	var candidateNodes []string
	for nodeName := range publishNodeSet {
		if nodesWithAnyReplica[nodeName] {
			continue
		}
		zone, ok := nodeNameToZone[nodeName]
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

	limit := len(candidateNodes)
	if len(unscheduledDiskfulReplicas) < limit {
		limit = len(unscheduledDiskfulReplicas)
	}

	for i := 0; i < limit; i++ {
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

		if len(candidatesInNewZones) == 0 {
			return nil
		}

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
