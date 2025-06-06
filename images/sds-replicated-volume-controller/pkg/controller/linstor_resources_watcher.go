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

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	lapi "github.com/LINBIT/golinstor/client"
	core "k8s.io/api/core/v1"
	v1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/images/sds-replicated-volume-controller/pkg/logger"
)

const (
	linstorResourcesWatcherCtrlName         = "linstor-resources-watcher-controller"
	missMatchedLabel                        = "storage.deckhouse.io/linstor-settings-mismatch"
	unableToSetQuorumMinimumRedundancyLabel = "storage.deckhouse.io/unable-to-set-quorum-minimum-redundancy"
	pvNotEnoughReplicasLabel                = "storage.deckhouse.io/pv-not-enough-replicas"
	PVCSIDriver                             = "replicated.csi.storage.deckhouse.io"
	replicasOnSameRGKey                     = "replicas_on_same"
	replicasOnDifferentRGKey                = "replicas_on_different"
	ReplicatedCSIProvisioner                = "replicated.csi.storage.deckhouse.io"
	quorumWithPrefixRDKey                   = "DrbdOptions/Resource/quorum"
	quorumMinimumRedundancyWithoutPrefixKey = "quorum-minimum-redundancy"
	quorumMinimumRedundancyWithPrefixRGKey  = "DrbdOptions/Resource/quorum-minimum-redundancy"
	QuorumMinimumRedundancyWithPrefixSCKey  = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/quorum-minimum-redundancy"
	replicasOnSameSCKey                     = "replicasOnSame"
	replicasOnDifferentSCKey                = "replicasOnDifferent"
	placementCountSCKey                     = "placementCount"
	storagePoolSCKey                        = "storagePool"
	autoplaceTarget                         = "AutoplaceTarget"
)

var (
	scParamsMatchRGProps = []string{
		"auto-quorum", "on-no-data-accessible", "on-suspended-primary-outdated", "rr-conflict", quorumMinimumRedundancyWithoutPrefixKey,
	}

	scParamsMatchRGSelectFilter = []string{
		replicasOnSameSCKey, replicasOnDifferentSCKey, placementCountSCKey, storagePoolSCKey,
	}

	disklessFlags = []string{"DRBD_DISKLESS", "DISKLESS", "TIE_BREAKER"}

	badLabels = []string{missMatchedLabel, unableToSetQuorumMinimumRedundancyLabel}
)

func NewLinstorResourcesWatcher(
	mgr manager.Manager,
	lc *lapi.Client,
	interval int,
	log logger.Logger,
) {
	cl := mgr.GetClient()
	ctx := context.Background()

	log.Info(fmt.Sprintf("[NewLinstorResourcesWatcher] the controller %s starts the work", linstorResourcesWatcherCtrlName))

	go func() {
		for {
			time.Sleep(time.Second * time.Duration(interval))
			log.Info("[NewLinstorResourcesWatcher] starts reconcile")

			runLinstorResourcesReconcile(ctx, log, cl, lc)

			log.Info("[NewLinstorResourcesWatcher] ends reconcile")
		}
	}()
}

func runLinstorResourcesReconcile(
	ctx context.Context,
	log logger.Logger,
	cl client.Client,
	lc *lapi.Client,
) {
	scs, err := GetStorageClasses(ctx, cl)
	if err != nil {
		log.Error(err, "[runLinstorResourcesReconcile] unable to get Kubernetes Storage Classes")
		return
	}

	scMap := make(map[string]v1.StorageClass, len(scs))
	for _, sc := range scs {
		scMap[sc.Name] = sc
	}

	rds, err := lc.ResourceDefinitions.GetAll(ctx, lapi.RDGetAllRequest{})
	if err != nil {
		log.Error(err, "[runLinstorResourcesReconcile] unable to get Linstor Resource Definitions")
		return
	}

	rdMap := make(map[string]lapi.ResourceDefinitionWithVolumeDefinition, len(rds))
	for _, rd := range rds {
		rdMap[rd.Name] = rd
	}

	rgs, err := lc.ResourceGroups.GetAll(ctx)
	if err != nil {
		log.Error(err, "[runLinstorResourcesReconcile] unable to get Linstor Resource Groups")
		return
	}

	rgMap := make(map[string]lapi.ResourceGroup, len(rgs))
	for _, rg := range rgs {
		rgMap[rg.Name] = rg
	}

	pvs, err := GetListPV(ctx, cl)
	if err != nil {
		log.Error(err, "[runLinstorResourcesReconcile] unable to get Persistent Volumes")
		return
	}

	pvList := make([]*core.PersistentVolume, 0)
	for i := range pvs {
		pv := &pvs[i]
		if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != PVCSIDriver {
			continue
		}
		if pv.Labels == nil {
			pv.Labels = make(map[string]string)
		}
		pvList = append(pvList, pv)
	}

	resMap := make(map[string][]lapi.Resource, len(rdMap))
	for name := range rdMap {
		res, err := lc.Resources.GetAll(ctx, name)
		if err != nil {
			log.Error(err, fmt.Sprintf("[runLinstorResourcesReconcile] unable to get Linstor Resources, name: %s", name))
			return
		}
		resMap[name] = res
	}

	ReconcileParams(ctx, log, cl, lc, scMap, rdMap, rgMap, pvList)
	ReconcileTieBreaker(ctx, log, lc, rdMap, rgMap, resMap)
	ReconcilePVReplicas(ctx, log, cl, lc, rdMap, rgMap, resMap, pvList)
}

func ReconcileParams(
	ctx context.Context,
	log logger.Logger,
	cl client.Client,
	lc *lapi.Client,
	scs map[string]v1.StorageClass,
	rds map[string]lapi.ResourceDefinitionWithVolumeDefinition,
	rgs map[string]lapi.ResourceGroup,
	pvs []*core.PersistentVolume,
) {
	log.Info("[ReconcileParams] starts work")

	for _, pv := range pvs {
		sc := scs[pv.Spec.StorageClassName]
		rd := rds[pv.Name]
		RGName := rd.ResourceGroupName
		rg := rgs[RGName]
		log.Debug(fmt.Sprintf("[ReconcileParams] PV: %s, SC: %s, RG: %s", pv.Name, sc.Name, rg.Name))

		if missMatched := getMissMatchedParams(sc, rg); len(missMatched) > 0 {
			log.Info(fmt.Sprintf("[ReconcileParams] the Kubernetes Storage Class %s and the Linstor Resource Group %s have missmatched params."+
				" The corresponding PV %s will have the special missmatched label %s if needed", sc.Name, rg.Name, pv.Name, missMatchedLabel))
			log.Info(fmt.Sprintf("[ReconcileParams] missmatched Storage Class params: %s", strings.Join(missMatched, ",")))

			labelsToAdd := make(map[string]string)

			if slices.Contains(missMatched, quorumMinimumRedundancyWithoutPrefixKey) && sc.Parameters[QuorumMinimumRedundancyWithPrefixSCKey] != "" {
				log.Info(fmt.Sprintf("[ReconcileParams] the quorum-minimum-redundancy value is set in the Storage Class %s, value: %s, but it is not match the Resource Group %s value %s", sc.Name, sc.Parameters[QuorumMinimumRedundancyWithPrefixSCKey], rg.Name, rg.Props[quorumMinimumRedundancyWithPrefixRGKey]))
				log.Info(fmt.Sprintf("[ReconcileParams] the quorum-minimum-redundancy value will be set to the Resource Group %s, value: %s", rg.Name, sc.Parameters[QuorumMinimumRedundancyWithPrefixSCKey]))
				err := setQuorumMinimumRedundancy(ctx, lc, sc.Parameters[QuorumMinimumRedundancyWithPrefixSCKey], rg.Name)

				if err != nil {
					log.Error(err, fmt.Sprintf("[ReconcileParams] unable to set the quorum-minimum-redundancy value, name: %s", pv.Name))
					labelsToAdd = map[string]string{unableToSetQuorumMinimumRedundancyLabel: "true"}
				} else {
					rgWithNewValue, err := lc.ResourceGroups.Get(ctx, rg.Name)
					if err != nil {
						log.Error(err, fmt.Sprintf("[ReconcileParams] unable to get the Resource Group, name: %s", rg.Name))
					} else {
						rgs[RGName] = rgWithNewValue
						missMatched = getMissMatchedParams(sc, rgs[RGName])
					}
				}
			}

			if len(missMatched) > 0 {
				labelsToAdd = map[string]string{missMatchedLabel: "true"}
			}
			setLabelsIfNeeded(ctx, log, cl, pv, labelsToAdd)
		} else {
			log.Info(fmt.Sprintf("[ReconcileParams] the Kubernetes Storage Class %s and the Linstor Resource Group %s have equal params", sc.Name, rg.Name))
			setLabelsIfNeeded(ctx, log, cl, pv, nil)
		}

		setQuorumIfNeeded(ctx, log, lc, sc, rd)
	}

	log.Info("[ReconcileParams] ends work")
}

func ReconcilePVReplicas(
	ctx context.Context,
	log logger.Logger,
	cl client.Client,
	lc *lapi.Client,
	rds map[string]lapi.ResourceDefinitionWithVolumeDefinition,
	rgs map[string]lapi.ResourceGroup,
	res map[string][]lapi.Resource,
	pvs []*core.PersistentVolume,
) {
	log.Info("[ReconcilePVReplicas] starts work")

	for _, pv := range pvs {
		RGName := rds[pv.Name].ResourceGroupName
		rg := rgs[RGName]
		log.Debug(fmt.Sprintf("[ReconcilePVReplicas] PV: %s, RG: %s", pv.Name, rg.Name))

		resources := res[pv.Name]
		replicasErrLevel, err := checkPVMinReplicasCount(ctx, log, lc, rg, resources)
		if err != nil {
			log.Error(err, "[ReconcilePVReplicas] unable to validate replicas count")
			continue
		}

		origLabelVal, exists := pv.Labels[pvNotEnoughReplicasLabel]
		log.Debug(fmt.Sprintf("[ReconcilePVReplicas] Update label \"%s\", old: \"%s\", new: \"%s\"", pvNotEnoughReplicasLabel, origLabelVal, replicasErrLevel))

		if replicasErrLevel == "" && exists {
			delete(pv.Labels, pvNotEnoughReplicasLabel)
			if err := cl.Update(ctx, pv); err != nil {
				log.Error(err, fmt.Sprintf("[ReconcilePVReplicas] unable to update the PV, name: %s", pv.Name))
			}
		}
		if replicasErrLevel != "" && replicasErrLevel != origLabelVal {
			pv.Labels[pvNotEnoughReplicasLabel] = replicasErrLevel
			if err := cl.Update(ctx, pv); err != nil {
				log.Error(err, fmt.Sprintf("[ReconcilePVReplicas] unable to update the PV, name: %s", pv.Name))
			}
		}
	}

	log.Info("[ReconcilePVReplicas] ends work")
}

func checkPVMinReplicasCount(
	ctx context.Context,
	log logger.Logger,
	lc *lapi.Client,
	rg lapi.ResourceGroup,
	resources []lapi.Resource,
) (string, error) {
	placeCount := int(rg.SelectFilter.PlaceCount)
	if placeCount <= 0 {
		return "", nil
	}

	upVols := 0
	for _, r := range resources {
		volList, err := lc.Resources.GetVolumes(ctx, r.Name, r.NodeName)
		if err != nil {
			log.Warning(fmt.Sprintf("[checkPVMinReplicasCount] unable to get Linstor Resources Volumes, name: %s, node: %s", r.Name, r.NodeName))
			return "", err
		}

		for _, v := range volList {
			if v.State.DiskState == "UpToDate" {
				upVols++
			}
		}
	}

	switch {
	case upVols >= placeCount:
		return "", nil
	case upVols <= 1:
		return "fatal", nil
	case (upVols*100)/placeCount <= 50:
		return "error", nil
	default:
		return "warning", nil
	}
}

func ReconcileTieBreaker(
	ctx context.Context,
	log logger.Logger,
	lc *lapi.Client,
	rds map[string]lapi.ResourceDefinitionWithVolumeDefinition,
	rgs map[string]lapi.ResourceGroup,
	res map[string][]lapi.Resource,
) {
	log.Info("[ReconcileTieBreaker] starts work")

	var (
		nodes []lapi.Node
		err   error
	)
	for name, resources := range res {
		if len(resources) == 0 {
			log.Warning(fmt.Sprintf("[ReconcileTieBreaker] no actual Linstor Resources for the Resource Definition, name: %s", name))
			continue
		}

		if len(resources)%2 != 0 {
			log.Info(fmt.Sprintf("[ReconcileTieBreaker] the Linstor Resource, name: %s has odd replicas count. No need to create diskless one", name))
			continue
		}

		if hasDisklessReplica(resources) {
			log.Info(fmt.Sprintf("[ReconcileTieBreaker] the Linstor Resource, name: %s has already have a diskless replica. No need to create one", name))
			continue
		}

		if len(nodes) == 0 {
			nodes, err = lc.Nodes.GetAll(ctx)
			if err != nil || len(nodes) == 0 {
				log.Error(err, "[getNodeForTieBreaker] unable to get all Linstor nodes")
				return
			}
		}

		nodeName, err := getNodeForTieBreaker(log, nodes, resources, rds, rgs)
		if err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileTieBreaker] unable to get a node for a Tie-breaker replica for the Linstor Resource, name: %s", name))
			continue
		}

		err = createTieBreaker(ctx, lc, name, nodeName)
		if err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileTieBreaker] unable to create a diskless replica on the node %s for the Linstor Resource, name: %s", nodeName, name))
			continue
		}

		log.Info(fmt.Sprintf("[ReconcileTieBreaker] a diskless replica for the Linstor Resource, name: %s has been successfully created", name))
	}

	log.Info("[ReconcileTieBreaker] ends work")
}

func createTieBreaker(ctx context.Context, lc *lapi.Client, resourceName, nodeName string) error {
	resCreate := lapi.ResourceCreate{
		Resource: lapi.Resource{
			Name:        resourceName,
			NodeName:    nodeName,
			Flags:       disklessFlags,
			LayerObject: lapi.ResourceLayer{},
		},
	}

	err := lc.Resources.Create(ctx, resCreate)
	if err != nil {
		return err
	}

	return nil
}

func getNodeForTieBreaker(
	log logger.Logger,
	nodes []lapi.Node,
	resources []lapi.Resource,
	rds map[string]lapi.ResourceDefinitionWithVolumeDefinition,
	rgs map[string]lapi.ResourceGroup,
) (string, error) {
	unusedNodes := filterOutUsedNodes(nodes, resources)
	for _, node := range unusedNodes {
		log.Trace(fmt.Sprintf("[getNodeForTieBreaker] resource %s does not use a node %s", resources[0].Name, node.Name))
	}

	rg := getResourceGroupByResource(resources[0].Name, rds, rgs)

	if key, exist := rg.Props[replicasOnSameRGKey]; exist {
		unusedNodes = filterNodesByReplicasOnSame(unusedNodes, key)
		for _, node := range unusedNodes {
			log.Trace(fmt.Sprintf("[getNodeForTieBreaker] node %s has passed the filter by ReplicasOnSame key", node.Name))
		}
	}

	if key, exist := rg.Props[replicasOnDifferentRGKey]; exist {
		values := getReplicasOnDifferentValues(nodes, resources, key)
		unusedNodes = filterNodesByReplicasOnDifferent(unusedNodes, key, values)
		for _, node := range unusedNodes {
			log.Trace(fmt.Sprintf("[getNodeForTieBreaker] node %s has passed the filter by ReplicasOnDifferent key", node.Name))
		}
	}

	unusedNodes = filterNodesByAutoplaceTarget(unusedNodes)
	for _, node := range unusedNodes {
		log.Trace(fmt.Sprintf("[getNodeForTieBreaker] node %s has passed the filter by AutoplaceTarget key", node.Name))
	}

	if len(unusedNodes) == 0 {
		err := errors.New("no any node is available to create tie-breaker")
		log.Error(err, fmt.Sprintf("[getNodeForTieBreaker] unable to create tie-breaker for resource, name: %s", resources[0].Name))
		return "", err
	}

	return unusedNodes[0].Name, nil
}

func filterNodesByAutoplaceTarget(nodes []lapi.Node) []lapi.Node {
	filtered := make([]lapi.Node, 0, len(nodes))

	for _, node := range nodes {
		if val, exist := node.Props[autoplaceTarget]; exist &&
			val == "false" {
			continue
		}

		filtered = append(filtered, node)
	}

	return filtered
}

func filterNodesByReplicasOnDifferent(nodes []lapi.Node, key string, values []string) []lapi.Node {
	filtered := make([]lapi.Node, 0, len(nodes))

	for _, node := range nodes {
		if value, exist := node.Props[key]; exist {
			if !slices.Contains(values, value) {
				filtered = append(filtered, node)
			}
		}
	}

	return filtered
}

func getReplicasOnDifferentValues(nodes []lapi.Node, resources []lapi.Resource, key string) []string {
	values := make([]string, 0, len(resources))
	resNodes := make(map[string]struct{}, len(resources))

	for _, resource := range resources {
		resNodes[resource.NodeName] = struct{}{}
	}

	for _, node := range nodes {
		if _, used := resNodes[node.Name]; used {
			values = append(values, node.Props[key])
		}
	}

	return values
}

func filterNodesByReplicasOnSame(nodes []lapi.Node, key string) []lapi.Node {
	filtered := make([]lapi.Node, 0, len(nodes))

	for _, node := range nodes {
		if _, exist := node.Props[key]; exist {
			filtered = append(filtered, node)
		}
	}

	return filtered
}

func getResourceGroupByResource(resourceName string, rds map[string]lapi.ResourceDefinitionWithVolumeDefinition, rgs map[string]lapi.ResourceGroup) lapi.ResourceGroup {
	return rgs[rds[resourceName].ResourceGroupName]
}

func filterOutUsedNodes(nodes []lapi.Node, resources []lapi.Resource) []lapi.Node {
	unusedNodes := make([]lapi.Node, 0, len(nodes))
	resNodes := make(map[string]struct{}, len(resources))

	for _, resource := range resources {
		resNodes[resource.NodeName] = struct{}{}
	}

	for _, node := range nodes {
		if _, used := resNodes[node.Name]; !used {
			unusedNodes = append(unusedNodes, node)
		}
	}

	return unusedNodes
}

func hasDisklessReplica(resources []lapi.Resource) bool {
	for _, resource := range resources {
		for _, flag := range resource.Flags {
			if slices.Contains(disklessFlags, flag) {
				return true
			}
		}
	}

	return false
}

func GetStorageClasses(ctx context.Context, cl client.Client) ([]v1.StorageClass, error) {
	listStorageClasses := &v1.StorageClassList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageClass",
			APIVersion: "storage.k8s.io/v1",
		},
	}
	err := cl.List(ctx, listStorageClasses)
	if err != nil {
		return nil, err
	}
	return listStorageClasses.Items, nil
}

func GetListPV(ctx context.Context, cl client.Client) ([]core.PersistentVolume, error) {
	PersistentVolumeList := &core.PersistentVolumeList{}
	err := cl.List(ctx, PersistentVolumeList)
	if err != nil {
		return nil, err
	}
	return PersistentVolumeList.Items, nil
}

func removePrefixes(params map[string]string) map[string]string {
	tmp := make(map[string]string, len(params))
	for k, v := range params {
		tmpKey := strings.Split(k, "/")
		if len(tmpKey) > 0 {
			newKey := tmpKey[len(tmpKey)-1]
			tmp[newKey] = v
		}
	}
	return tmp
}

func getRGReplicasValue(value string) string {
	tmp := strings.Split(value, "/")
	l := len(tmp)
	if l > 1 {
		return fmt.Sprintf("%s/%s", tmp[l-2], tmp[l-1])
	}

	return strings.Join(tmp, "")
}

func getMissMatchedParams(sc v1.StorageClass, rg lapi.ResourceGroup) []string {
	missMatched := make([]string, 0, len(sc.Parameters))

	scParams := removePrefixes(sc.Parameters)
	rgProps := removePrefixes(rg.Props)

	for _, param := range scParamsMatchRGProps {
		if scParams[param] != rgProps[param] {
			missMatched = append(missMatched, param)
		}
	}

	for _, param := range scParamsMatchRGSelectFilter {
		switch param {
		case replicasOnSameSCKey:
			replicasOnSame := ""
			if len(rg.SelectFilter.ReplicasOnSame) != 0 {
				replicasOnSame = getRGReplicasValue(rg.SelectFilter.ReplicasOnSame[0])
			}
			if scParams[param] != replicasOnSame {
				missMatched = append(missMatched, param)
			}

		case replicasOnDifferentSCKey:
			replicasOnDifferent := ""
			if len(rg.SelectFilter.ReplicasOnDifferent) != 0 {
				replicasOnDifferent = getRGReplicasValue(rg.SelectFilter.ReplicasOnDifferent[0])
			}
			if scParams[param] != replicasOnDifferent {
				missMatched = append(missMatched, param)
			}
		case placementCountSCKey:
			placeCount := strconv.Itoa(int(rg.SelectFilter.PlaceCount))
			if scParams[param] != placeCount {
				missMatched = append(missMatched, param)
			}
		case storagePoolSCKey:
			if scParams[param] != rg.SelectFilter.StoragePool {
				missMatched = append(missMatched, param)
			}
		}
	}

	return missMatched
}

func setQuorumMinimumRedundancy(ctx context.Context, lc *lapi.Client, value, rgName string) error {
	quorumMinimumRedundancy, err := strconv.Atoi(value)
	if err != nil {
		return err
	}

	err = lc.ResourceGroups.Modify(ctx, rgName, lapi.ResourceGroupModify{
		OverrideProps: map[string]string{
			quorumMinimumRedundancyWithPrefixRGKey: strconv.Itoa(quorumMinimumRedundancy),
		},
	})

	return err
}

func setLabelsIfNeeded(
	ctx context.Context,
	log logger.Logger,
	cl client.Client,
	pv *core.PersistentVolume,
	labelsToAdd map[string]string,
) {
	log.Debug(fmt.Sprintf("[setLabelsIfNeeded] Original labels: %+v", pv.Labels))

	newLabels := pv.Labels
	updated := false

	for _, label := range badLabels {
		if _, exists := newLabels[label]; exists {
			delete(newLabels, label)
			updated = true
		}
	}

	for k, v := range labelsToAdd {
		if origVal, exists := newLabels[k]; !exists || origVal != v {
			newLabels[k] = v
			updated = true
		}
	}

	if updated {
		log.Debug(fmt.Sprintf("[ReconcileParams] New labels: %+v", newLabels))

		if err := cl.Update(ctx, pv); err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileParams] unable to update the PV, name: %s", pv.Name))
		}
	}
}

func setQuorumIfNeeded(ctx context.Context, log logger.Logger, lc *lapi.Client, sc v1.StorageClass, rd lapi.ResourceDefinitionWithVolumeDefinition) {
	rdPropQuorum := rd.Props[quorumWithPrefixRDKey]
	if sc.Provisioner == ReplicatedCSIProvisioner &&
		sc.Parameters[StorageClassPlacementCountKey] != "1" &&
		slices.Contains([]string{"off", "1", ""}, rdPropQuorum) {
		log.Info(fmt.Sprintf("[setQuorumIfNeeded] Resource Definition %s quorum value will be set to 'majority'", rd.Name))

		err := lc.ResourceDefinitions.Modify(ctx, rd.Name, lapi.GenericPropsModify{
			OverrideProps: map[string]string{
				quorumWithPrefixRDKey: "majority",
			},
		})
		if err != nil {
			log.Error(err, fmt.Sprintf("[setQuorumIfNeeded] unable to set the quorum value for Resource Definition %s", rd.Name))
		}
	}
}
