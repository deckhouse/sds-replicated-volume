/*
Copyright 2023 Flant JSC

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
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"time"

	lclient "github.com/LINBIT/golinstor/client"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"sds-replicated-volume-controller/config"
	"sds-replicated-volume-controller/pkg/logger"
)

const (
	LinstorDriverName = "replicated.csi.storage.deckhouse.io"

	LinstorNodeControllerName          = "linstor-node-controller"
	LinstorControllerType              = "CONTROLLER"
	LinstorSatelliteType               = "SATELLITE"
	LinstorOnlineStatus                = "ONLINE"
	LinstorOfflineStatus               = "OFFLINE"
	LinstorNodePort                    = 3367  //
	LinstorEncryptionType              = "SSL" // "Plain"
	reachableTimeout                   = 10 * time.Second
	SdsReplicatedVolumeNodeSelectorKey = "storage.deckhouse.io/sds-replicated-volume-node"

	LinbitHostnameLabelKey          = "linbit.com/hostname"
	LinbitStoragePoolPrefixLabelKey = "linbit.com/sp-"

	SdsHostnameLabelKey          = "storage.deckhouse.io/sds-replicated-volume-hostname"
	SdsStoragePoolPrefixLabelKey = "storage.deckhouse.io/sds-replicated-volume-sp-"

	InternalIP = "InternalIP"
)

var (
	drbdNodeSelector = map[string]string{SdsReplicatedVolumeNodeSelectorKey: ""}

	AllowedLabels = []string{
		"kubernetes.io/hostname",
		"topology.kubernetes.io/region",
		"topology.kubernetes.io/zone",
		"registered-by",
		SdsHostnameLabelKey,
		SdsReplicatedVolumeNodeSelectorKey,
	}

	AllowedPrefixes = []string{
		"class.storage.deckhouse.io/",
		SdsStoragePoolPrefixLabelKey,
	}
)

func NewLinstorNode(
	mgr manager.Manager,
	lc *lclient.Client,
	configSecretName string,
	interval int,
	log logger.Logger,
) (controller.Controller, error) {
	cl := mgr.GetClient()

	c, err := controller.New(LinstorNodeControllerName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			if request.Name == configSecretName {
				log.Info("Start reconcile of LINSTOR nodes.")
				err := reconcileLinstorNodes(ctx, cl, lc, log, request.Namespace, request.Name, drbdNodeSelector)
				if err != nil {
					log.Error(nil, "Failed reconcile of LINSTOR nodes")
				} else {
					log.Info("END reconcile of LINSTOR nodes.")
				}

				return reconcile.Result{
					RequeueAfter: time.Duration(interval) * time.Second,
				}, nil
			}

			return reconcile.Result{}, nil
		}),
	})

	if err != nil {
		return nil, err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &v1.Secret{}, &handler.TypedEnqueueRequestForObject[*v1.Secret]{}))

	return c, err
}

func reconcileLinstorNodes(
	ctx context.Context,
	cl client.Client,
	lc *lclient.Client,
	log logger.Logger,
	secretNamespace string,
	secretName string,
	drbdNodeSelector map[string]string,
) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, reachableTimeout)
	defer cancel()

	configSecret, err := GetKubernetesSecretByName(ctx, cl, secretName, secretNamespace)
	if err != nil {
		log.Error(err, "Failed get secret:"+secretNamespace+"/"+secretName)
		return err
	}

	configNodeSelector, err := GetNodeSelectorFromConfig(*configSecret)
	if err != nil {
		log.Error(err, "Failed get node selector from secret:"+secretName+"/"+secretNamespace)
		return err
	}
	selectedKubernetesNodes, err := GetKubernetesNodesBySelector(ctx, cl, configNodeSelector)
	if err != nil {
		log.Error(err, "Failed get nodes from Kubernetes by selector:"+fmt.Sprint(configNodeSelector))
		return err
	}

	linstorSatelliteNodes, linstorControllerNodes, err := GetLinstorNodes(timeoutCtx, lc)
	if err != nil {
		log.Error(err, "Failed get LINSTOR nodes")
		return err
	}

	replicatedStorageClasses := srv.ReplicatedStorageClassList{}
	err = cl.List(ctx, &replicatedStorageClasses)
	if err != nil {
		log.Error(err, "Failed get DRBD storage classes")
		return err
	}

	if len(selectedKubernetesNodes.Items) != 0 {
		err = AddOrConfigureDRBDNodes(ctx, cl, lc, log, selectedKubernetesNodes, linstorSatelliteNodes, replicatedStorageClasses, drbdNodeSelector)
		if err != nil {
			log.Error(err, "Failed add DRBD nodes:")
			return err
		}
	} else {
		log.Warning("reconcileLinstorNodes: There are not any Kubernetes nodes for LINSTOR that can be selected by selector:" + fmt.Sprint(configNodeSelector))
	}

	err = renameLinbitLabels(ctx, cl, selectedKubernetesNodes.Items)
	if err != nil {
		log.Error(err, "[reconcileLinstorNodes] unable to rename linbit labels")
		return err
	}

	err = ReconcileCSINodeLabels(ctx, cl, log, selectedKubernetesNodes.Items)
	if err != nil {
		log.Error(err, "[reconcileLinstorNodes] unable to reconcile CSI node labels")
		return err
	}

	// Remove logic
	allKubernetesNodes, err := GetAllKubernetesNodes(ctx, cl)
	if err != nil {
		log.Error(err, "Failed get all nodes from Kubernetes")
		return err
	}
	drbdNodesToRemove := DiffNodeLists(allKubernetesNodes, selectedKubernetesNodes)

	err = removeDRBDNodes(ctx, cl, log, drbdNodesToRemove, linstorSatelliteNodes, replicatedStorageClasses, drbdNodeSelector)
	if err != nil {
		log.Error(err, "Failed remove DRBD nodes:")
		return err
	}

	err = removeLinstorControllerNodes(ctx, lc, log, linstorControllerNodes)
	if err != nil {
		log.Error(err, "Failed remove LINSTOR controller nodes:")
		return err
	}

	return nil
}

func ReconcileCSINodeLabels(ctx context.Context, cl client.Client, log logger.Logger, nodes []v1.Node) error {
	nodeLabels := make(map[string]map[string]string, len(nodes))
	for _, node := range nodes {
		nodeLabels[node.Name] = node.Labels
	}

	csiList := &storagev1.CSINodeList{}
	err := cl.List(ctx, csiList)
	if err != nil {
		log.Error(err, "[syncCSINodesLabels] unable to list CSI nodes")
		return err
	}

	for _, csiNode := range csiList.Items {
		log.Debug(fmt.Sprintf("[syncCSINodesLabels] starts the topology keys check for a CSI node %s", csiNode.Name))

		var (
			kubeNodeLabelsToSync = make(map[string]struct{}, len(nodeLabels[csiNode.Name]))
			syncedCSIDriver      storagev1.CSINodeDriver
			csiTopoKeys          map[string]struct{}
		)

		for _, driver := range csiNode.Spec.Drivers {
			log.Trace(fmt.Sprintf("[syncCSINodesLabels] CSI node %s has a driver %s", csiNode.Name, driver.Name))
			if driver.Name == LinstorDriverName {
				syncedCSIDriver = driver
				csiTopoKeys = make(map[string]struct{}, len(driver.TopologyKeys))

				for _, topoKey := range driver.TopologyKeys {
					csiTopoKeys[topoKey] = struct{}{}
				}
			}
		}

		if syncedCSIDriver.Name == "" {
			log.Debug(fmt.Sprintf("[syncCSINodesLabels] CSI node %s does not have a driver %s", csiNode.Name, LinstorDriverName))
			continue
		}

		for nodeLabel := range nodeLabels[csiNode.Name] {
			if slices.Contains(AllowedLabels, nodeLabel) {
				kubeNodeLabelsToSync[nodeLabel] = struct{}{}
				continue
			}

			for _, prefix := range AllowedPrefixes {
				if strings.HasPrefix(nodeLabel, prefix) {
					kubeNodeLabelsToSync[nodeLabel] = struct{}{}
				}
			}
		}

		if reflect.DeepEqual(kubeNodeLabelsToSync, csiTopoKeys) {
			log.Debug(fmt.Sprintf("[syncCSINodesLabels] CSI node %s topology keys is synced with its corresponding node", csiNode.Name))
			return nil
		}
		log.Debug(fmt.Sprintf("[syncCSINodesLabels] CSI node %s topology keys need to be synced with its corresponding node labels", csiNode.Name))

		syncedTopologyKeys := make([]string, 0, len(kubeNodeLabelsToSync))
		for label := range kubeNodeLabelsToSync {
			syncedTopologyKeys = append(syncedTopologyKeys, label)
		}
		log.Trace(fmt.Sprintf("[syncCSINodesLabels] final topology keys for a CSI node %s: %v", csiNode.Name, syncedTopologyKeys))
		syncedCSIDriver.TopologyKeys = syncedTopologyKeys

		err = removeDriverFromCSINode(ctx, cl, &csiNode, syncedCSIDriver.Name)
		if err != nil {
			log.Error(err, fmt.Sprintf("[syncCSINodesLabels] unable to remove driver %s from CSI node %s", syncedCSIDriver.Name, csiNode.Name))
			return err
		}
		log.Debug(fmt.Sprintf("[syncCSINodesLabels] removed old driver %s of a CSI node %s", syncedCSIDriver.Name, csiNode.Name))

		err = addDriverToCSINode(ctx, cl, &csiNode, syncedCSIDriver)
		if err != nil {
			log.Error(err, fmt.Sprintf("[syncCSINodesLabels] unable to add driver %s to a CSI node %s", syncedCSIDriver.Name, csiNode.Name))
			return err
		}

		log.Debug(fmt.Sprintf("[syncCSINodesLabels] add updated driver %s of the CSI node %s", syncedCSIDriver.Name, csiNode.Name))
		log.Debug(fmt.Sprintf("[syncCSINodesLabels] successfully updated topology keys for CSI node %s", csiNode.Name))
	}

	return nil
}

func addDriverToCSINode(ctx context.Context, cl client.Client, csiNode *storagev1.CSINode, csiDriver storagev1.CSINodeDriver) error {
	csiNode.Spec.Drivers = append(csiNode.Spec.Drivers, csiDriver)
	err := cl.Update(ctx, csiNode)
	if err != nil {
		return err
	}

	return nil
}

func removeDriverFromCSINode(ctx context.Context, cl client.Client, csiNode *storagev1.CSINode, driverName string) error {
	for i, driver := range csiNode.Spec.Drivers {
		if driver.Name == driverName {
			csiNode.Spec.Drivers = slices.Delete(csiNode.Spec.Drivers, i, i+1)
		}
	}
	err := cl.Update(ctx, csiNode)
	if err != nil {
		return err
	}

	return nil
}

func renameLinbitLabels(ctx context.Context, cl client.Client, nodes []v1.Node) error {
	var err error
	for _, node := range nodes {
		shouldUpdate := false
		if value, exist := node.Labels[LinbitHostnameLabelKey]; exist {
			node.Labels[SdsHostnameLabelKey] = value
			delete(node.Labels, LinbitHostnameLabelKey)
			shouldUpdate = true
		}

		for k, v := range node.Labels {
			if strings.HasPrefix(k, LinbitStoragePoolPrefixLabelKey) {
				postfix, _ := strings.CutPrefix(k, LinbitStoragePoolPrefixLabelKey)

				sdsKey := SdsStoragePoolPrefixLabelKey + postfix
				node.Labels[sdsKey] = v
				delete(node.Labels, k)
				shouldUpdate = true
			}
		}

		if shouldUpdate {
			err = cl.Update(ctx, &node)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func removeDRBDNodes(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	drbdNodesToRemove v1.NodeList,
	linstorSatelliteNodes []lclient.Node,
	replicatedStorageClasses srv.ReplicatedStorageClassList,
	drbdNodeSelector map[string]string,
) error {
	for _, drbdNodeToRemove := range drbdNodesToRemove.Items {
		log.Info(fmt.Sprintf("Processing the node '%s' that does not match the user-defined selector.", drbdNodeToRemove.Name))
		log.Info(fmt.Sprintf("Checking if node '%s' is a LINSTOR node.", drbdNodeToRemove.Name))

		for _, linstorNode := range linstorSatelliteNodes {
			if drbdNodeToRemove.Name == linstorNode.Name {
				// #TODO: Should we add ConfigureDRBDNode here?
				log.Info(fmt.Sprintf("Detected a LINSTOR node '%s' that no longer matches the user-defined selector and needs to be removed. Initiating the deletion process.", drbdNodeToRemove.Name))
				log.Error(nil, "Warning! Delete logic not yet implemented. Removal of LINSTOR nodes is prohibited.")
				break
			}
		}
		log.Info(fmt.Sprintf("Reconciling labels for node '%s'", drbdNodeToRemove.Name))
		err := ReconcileKubernetesNodeLabels(ctx, cl, log, drbdNodeToRemove, replicatedStorageClasses, drbdNodeSelector, false)
		if err != nil {
			return fmt.Errorf("unable to reconcile labels for node %s: %w", drbdNodeToRemove.Name, err)
		}
	}

	return nil
}

func AddOrConfigureDRBDNodes(
	ctx context.Context,
	cl client.Client,
	lc *lclient.Client,
	log logger.Logger,
	selectedKubernetesNodes *v1.NodeList,
	linstorNodes []lclient.Node,
	replicatedStorageClasses srv.ReplicatedStorageClassList,
	drbdNodeSelector map[string]string,
) error {
	for _, selectedKubernetesNode := range selectedKubernetesNodes.Items {
		drbdNodeProperties := KubernetesNodeLabelsToProperties(selectedKubernetesNode.Labels)
		findMatch := false

		for _, linstorNode := range linstorNodes {
			if selectedKubernetesNode.Name == linstorNode.Name {
				findMatch = true
				err := ConfigureDRBDNode(ctx, lc, linstorNode, drbdNodeProperties)
				if err != nil {
					return fmt.Errorf("unable set drbd properties to node %s: %w", linstorNode.Name, err)
				}
				break
			}
		}

		err := ReconcileKubernetesNodeLabels(ctx, cl, log, selectedKubernetesNode, replicatedStorageClasses, drbdNodeSelector, true)
		if err != nil {
			return fmt.Errorf("unable to reconcile labels for node %s: %w", selectedKubernetesNode.Name, err)
		}

		if !findMatch {
			log.Info("AddOrConfigureDRBDNodes: Create LINSTOR node: " + selectedKubernetesNode.Name)
			err := CreateDRBDNode(ctx, lc, selectedKubernetesNode, drbdNodeProperties)
			if err != nil {
				return fmt.Errorf("unable to create LINSTOR node %s: %w", selectedKubernetesNode.Name, err)
			}
		}
	}

	return nil
}

func ConfigureDRBDNode(
	ctx context.Context,
	lc *lclient.Client,
	linstorNode lclient.Node,
	drbdNodeProperties map[string]string,
) error {
	needUpdate := false

	for newPropertyName, newPropertyValue := range drbdNodeProperties {
		existingProperyValue, exists := linstorNode.Props[newPropertyName]
		if !exists || existingProperyValue != newPropertyValue {
			needUpdate = true
			break
		}
	}

	var propertiesToDelete []string

	for existingPropertyName := range linstorNode.Props {
		if !strings.HasPrefix(existingPropertyName, "Aux/") {
			continue
		}

		_, exist := drbdNodeProperties[existingPropertyName]
		if !exist {
			propertiesToDelete = append(propertiesToDelete, existingPropertyName)
		}
	}

	if needUpdate || len(propertiesToDelete) != 0 {
		err := lc.Nodes.Modify(ctx, linstorNode.Name, lclient.NodeModify{
			GenericPropsModify: lclient.GenericPropsModify{
				OverrideProps: drbdNodeProperties,
				DeleteProps:   propertiesToDelete,
			},
		})
		if err != nil {
			return fmt.Errorf("unable to update node properties: %w", err)
		}
	}
	return nil
}

func CreateDRBDNode(
	ctx context.Context,
	lc *lclient.Client,
	selectedKubernetesNode v1.Node,
	drbdNodeProperties map[string]string,
) error {
	var internalAddress string
	for _, ad := range selectedKubernetesNode.Status.Addresses {
		if ad.Type == InternalIP {
			internalAddress = ad.Address
		}
	}

	newLinstorNode := lclient.Node{
		Name: selectedKubernetesNode.Name,
		Type: LinstorSatelliteType,
		NetInterfaces: []lclient.NetInterface{
			{
				Name:                    "default",
				Address:                 net.ParseIP(internalAddress),
				IsActive:                true,
				SatellitePort:           LinstorNodePort,
				SatelliteEncryptionType: LinstorEncryptionType,
			},
		},
		Props: drbdNodeProperties,
	}
	err := lc.Nodes.Create(ctx, newLinstorNode)
	return err
}

func KubernetesNodeLabelsToProperties(kubernetesNodeLabels map[string]string) map[string]string {
	properties := map[string]string{
		"Aux/registered-by": LinstorNodeControllerName,
	}

	isAllowed := func(label string) bool {
		if slices.Contains(AllowedLabels, label) {
			return true
		}

		for _, prefix := range AllowedPrefixes {
			if strings.HasPrefix(label, prefix) {
				return true
			}
		}

		return false
	}

	for labelKey, labelValue := range kubernetesNodeLabels {
		if isAllowed(labelKey) {
			properties[fmt.Sprintf("Aux/%s", labelKey)] = labelValue
		}
	}

	return properties
}

func GetKubernetesSecretByName(
	ctx context.Context,
	cl client.Client,
	secretName string,
	secretNamespace string,
) (*v1.Secret, error) {
	secret := &v1.Secret{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      secretName,
		Namespace: secretNamespace,
	}, secret)
	return secret, err
}

func GetKubernetesNodesBySelector(ctx context.Context, cl client.Client, nodeSelector map[string]string) (*v1.NodeList, error) {
	selectedK8sNodes := &v1.NodeList{}
	err := cl.List(ctx, selectedK8sNodes, client.MatchingLabels(nodeSelector))
	return selectedK8sNodes, err
}

func GetAllKubernetesNodes(ctx context.Context, cl client.Client) (*v1.NodeList, error) {
	allKubernetesNodes := &v1.NodeList{}
	err := cl.List(ctx, allKubernetesNodes)
	return allKubernetesNodes, err
}

func GetNodeSelectorFromConfig(secret v1.Secret) (map[string]string, error) {
	var secretConfig config.SdsReplicatedVolumeOperatorConfig
	err := yaml.Unmarshal(secret.Data["config"], &secretConfig)
	if err != nil {
		return nil, err
	}
	nodeSelector := secretConfig.NodeSelector
	return nodeSelector, err
}

func DiffNodeLists(leftList, rightList *v1.NodeList) v1.NodeList {
	var diff v1.NodeList

	for _, leftNode := range leftList.Items {
		if !ContainsNode(rightList, leftNode) {
			diff.Items = append(diff.Items, leftNode)
		}
	}
	return diff
}

func ContainsNode(nodeList *v1.NodeList, node v1.Node) bool {
	for _, item := range nodeList.Items {
		if item.Name == node.Name {
			return true
		}
	}
	return false
}

func GetLinstorNodes(ctx context.Context, lc *lclient.Client) ([]lclient.Node, []lclient.Node, error) {
	linstorNodes, err := lc.Nodes.GetAll(ctx, &lclient.ListOpts{})
	if err != nil {
		return nil, nil, err
	}

	linstorControllerNodes := make([]lclient.Node, 0, len(linstorNodes))
	linstorSatelliteNodes := make([]lclient.Node, 0, len(linstorNodes))

	for _, linstorNode := range linstorNodes {
		if linstorNode.Type == LinstorControllerType {
			linstorControllerNodes = append(linstorControllerNodes, linstorNode)
		} else if linstorNode.Type == LinstorSatelliteType {
			linstorSatelliteNodes = append(linstorSatelliteNodes, linstorNode)
		}
	}

	return linstorSatelliteNodes, linstorControllerNodes, nil
}

func removeLinstorControllerNodes(
	ctx context.Context,
	lc *lclient.Client,
	log logger.Logger,
	linstorControllerNodes []lclient.Node,
) error {
	for _, linstorControllerNode := range linstorControllerNodes {
		log.Info("removeLinstorControllerNodes: Remove LINSTOR controller node: " + linstorControllerNode.Name)
		err := lc.Nodes.Delete(ctx, linstorControllerNode.Name)
		if err != nil {
			return err
		}
	}
	return nil
}

func ReconcileKubernetesNodeLabels(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	kubernetesNode v1.Node,
	replicatedStorageClasses srv.ReplicatedStorageClassList,
	drbdNodeSelector map[string]string,
	isDRBDNode bool,
) error {
	labelsToAdd := make(map[string]string)
	labelsToRemove := make(map[string]string)
	storageClassesLabelsForNode := make(map[string]string)

	if isDRBDNode {
		if !labels.Set(drbdNodeSelector).AsSelector().Matches(labels.Set(kubernetesNode.Labels)) {
			log.Info(fmt.Sprintf("Kubernetes node '%s' has not drbd label. Set it.", kubernetesNode.Name))
			labelsToAdd = labels.Merge(labelsToAdd, drbdNodeSelector)
		}

		storageClassesLabelsForNode = GetStorageClassesLabelsForNode(kubernetesNode, replicatedStorageClasses)
		for labelKey, labelValue := range storageClassesLabelsForNode {
			if _, existsInKubernetesNodeLabels := kubernetesNode.Labels[labelKey]; !existsInKubernetesNodeLabels {
				labelsToAdd[labelKey] = labelValue
			}
		}
	} else if labels.Set(drbdNodeSelector).AsSelector().Matches(labels.Set(kubernetesNode.Labels)) {
		log.Info(fmt.Sprintf("Kubernetes node: '%s' has a DRBD label but is no longer a DRBD node. Removing DRBD label.", kubernetesNode.Name))
		log.Error(nil, "Warning! Delete logic not yet implemented. Removal of DRBD label is prohibited.")
	}

	for labelKey := range kubernetesNode.Labels {
		if strings.HasPrefix(labelKey, StorageClassLabelKeyPrefix) {
			if _, existsInStorageClassesLabels := storageClassesLabelsForNode[labelKey]; !existsInStorageClassesLabels {
				labelsToRemove[labelKey] = ""
			}
		}
	}

	if len(labelsToAdd) == 0 && len(labelsToRemove) == 0 {
		return nil
	}

	if kubernetesNode.Labels == nil {
		kubernetesNode.Labels = make(map[string]string, len(labelsToAdd))
	}

	for k := range labelsToRemove {
		delete(kubernetesNode.Labels, k)
	}
	kubernetesNode.Labels = labels.Merge(kubernetesNode.Labels, labelsToAdd)

	log.Info(fmt.Sprintf("Reconciling labels for node '%s': adding %d labels (%v), removing %d labels(%v)", kubernetesNode.Name, len(labelsToAdd), labelsToAdd, len(labelsToRemove), labelsToRemove))
	err := cl.Update(ctx, &kubernetesNode)
	if err != nil {
		return err
	}
	return nil
}

func GetStorageClassesLabelsForNode(kubernetesNode v1.Node, replicatedStorageClasses srv.ReplicatedStorageClassList) map[string]string {
	storageClassesLabels := make(map[string]string)

	for _, replicatedStorageClass := range replicatedStorageClasses.Items {
		if replicatedStorageClass.Spec.Zones == nil {
			continue
		}
		for _, zone := range replicatedStorageClass.Spec.Zones {
			if zone == kubernetesNode.Labels[ZoneLabel] {
				storageClassLabelKey := fmt.Sprintf("%s/%s", StorageClassLabelKeyPrefix, replicatedStorageClass.Name)
				storageClassesLabels = labels.Merge(storageClassesLabels, map[string]string{storageClassLabelKey: ""})
				break
			}
		}
	}
	return storageClassesLabels
}
