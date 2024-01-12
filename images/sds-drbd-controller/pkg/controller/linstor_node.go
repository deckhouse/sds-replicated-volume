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
	"errors"
	"fmt"
	"golang.org/x/sync/errgroup"
	"net"
	sdsapi "sds-drbd-controller/api/v1alpha1"
	"strings"
	"time"

	lclient "github.com/LINBIT/golinstor/client"
	"github.com/go-logr/logr"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	LinstorNodeControllerName = "linstor-node-controller"
	LinstorControllerType     = "CONTROLLER"
	LinstorSatelliteType      = "SATELLITE"
	LinstorOnlineStatus       = "ONLINE"
	LinstorOfflineStatus      = "OFFLINE"
	LinstorNodePort           = 3367  //
	LinstorEncryptionType     = "SSL" // "Plain"
	reachableTimeout          = 10 * time.Second
	DRBDNodeSelectorKey       = "storage.deckhouse.io/sds-drbd-node"
	DisklessStoragePool       = "DfltDisklessStorPool"
)

var (
	drbdNodeSelector = map[string]string{DRBDNodeSelectorKey: ""}
)

func NewLinstorNode(
	ctx context.Context,
	mgr manager.Manager,
	lc *lclient.Client,
	configSecretName string,
	interval int,
) (controller.Controller, error) {
	cl := mgr.GetClient()
	log := mgr.GetLogger()

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
			}

			return reconcile.Result{
				RequeueAfter: time.Duration(interval) * time.Second,
			}, nil

		}),
	})

	if err != nil {
		return nil, err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &v1.Secret{}), &handler.EnqueueRequestForObject{})

	return c, err

}

func reconcileLinstorNodes(ctx context.Context, cl client.Client, lc *lclient.Client, log logr.Logger, secretNamespace string, secretName string, drbdNodeSelector map[string]string) error {
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

	drbdStorageClasses := sdsapi.DRBDStorageClassList{}
	err = cl.List(ctx, &drbdStorageClasses)
	if err != nil {
		log.Error(err, "Failed get DRBD storage classes")
		return err
	}

	if len(selectedKubernetesNodes.Items) != 0 {
		err = AddOrConfigureDRBDNodes(ctx, cl, lc, log, selectedKubernetesNodes, linstorSatelliteNodes, drbdStorageClasses, drbdNodeSelector)
		if err != nil {
			log.Error(err, "Failed add DRBD nodes:")
			return err
		}
	} else {
		log.Info("reconcileLinstorNodes: There are not any Kubernetes nodes for LINSTOR that can be selected by selector:" + fmt.Sprint(configNodeSelector)) //TODO: log.Warn
	}

	// Remove logic
	allKubernetesNodes, err := GetAllKubernetesNodes(ctx, cl)
	if err != nil {
		log.Error(err, "Failed get all nodes from Kubernetes")
		return err
	}
	drbdNodesToRemove := DiffNodeLists(allKubernetesNodes, selectedKubernetesNodes)

	err = removeDRBDNodes(ctx, cl, lc, log, drbdNodesToRemove, linstorSatelliteNodes, drbdStorageClasses, drbdNodeSelector)
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

func removeDRBDNodes(ctx context.Context, cl client.Client, lc *lclient.Client, log logr.Logger, drbdNodesToRemove v1.NodeList, linstorSatelliteNodes []lclient.Node, drbdStorageClasses sdsapi.DRBDStorageClassList, drbdNodeSelector map[string]string) error {
	// log.Debug("removeDRBDNodes: Start")
	for _, drbdNodeToRemove := range drbdNodesToRemove.Items {
		// log.Debug("removeDRBDNodes: Process Kubernetes node: " + drbdNodeToRemove.Name)

		for _, linstorNode := range linstorSatelliteNodes {
			if drbdNodeToRemove.Name == linstorNode.Name {
				// #TODO: Should we add ConfigureDRBDNode here?
				log.Info("Remove LINSTOR node: " + drbdNodeToRemove.Name)

				nodeName := drbdNodeToRemove.Name

				overrideProps := lclient.GenericPropsModify{OverrideProps: map[string]string{"AutoplaceTarget": "false"}}
				err := lc.Nodes.Modify(ctx, nodeName, lclient.NodeModify{GenericPropsModify: overrideProps})
				if err != nil {
					log.Error(err, "Warning! Couldnot set node autoplace prop to false")
				}

				// Get all resource from linstor controller
				resources := make([]lclient.Resource, 0, 50)
				resourcesGroups, err := lc.ResourceDefinitions.GetAll(ctx, lclient.RDGetAllRequest{})
				if err != nil {
					log.Error(err, "Warning! Couldnot recieve resource definitions list from linstor controller")
				}
				for _, rg := range resourcesGroups {
					rs, err := lc.Resources.GetAll(ctx, rg.Name)
					if err != nil {
						log.Error(err, "Warning! Couldnot recieve resource list from linstor controller")
						break
					}
					resources = append(resources, rs...)
				}

				nodeToBeDeleted := false
				// filter res by node and diskfull
				for _, res := range resources {
					if res.NodeName == nodeName && res.Props["StorPoolName"] != DisklessStoragePool {
						resDefinition, err := lc.ResourceDefinitions.Get(ctx, res.Name)
						if err != nil {
							log.Error(err, "could not get resource definition from controller", res.Name)
							break
						}

						resGroup, err := lc.ResourceGroups.Get(ctx, resDefinition.ResourceGroupName)
						if err != nil {
							log.Error(err, "could not get resource group from controller", resDefinition.ResourceGroupName)
							break
						}

						desiredRelicaCount := resGroup.SelectFilter.PlaceCount + 1
						isTiebrakerNeeded := desiredRelicaCount == 2
						// Only one that I can invent
						if isTiebrakerNeeded {
							desiredRelicaCount++
						}

						resVolumes, err := lc.ResourceDefinitions.GetVolumeDefinitions(ctx, res.Name)
						if err != nil {
							log.Error(err, "could not get resource volumes from controller", res.Name)
							break
						}
						resVolumesSizeKb := uint64(0)
						for _, resVol := range resVolumes {
							resVolumesSizeKb += resVol.SizeKib
						}

						// Get nodes from resource group and calc difference with available nodes
						allNodes, err := lc.Nodes.GetAll(ctx)
						if err != nil {
							log.Error(err, "could not nodes from controller")
							break
						}

						// Get available nodes with enough disk space
						//log.Info("What about all nodes?", "nodes", allNodes)
						//log.Info("Resource group", "res_group", resGroup)
						//log.Info("Resource", "res", resources)
						//log.Info("Resource definition", "res_def", resDefinition)

						nodesWithRes, err := getNodesByResourceName(ctx, lc, res.Name)
						if err != nil {
							log.Error(err, "could not get nodes from ctrl by resource name: "+res.Name)
							break
						}
						log.Info("Node by res name", "nodes", nodesWithRes)

						availableNodes := getAvailableNodes(allNodes, nodesWithRes)
						log.Info("Node by res name after 1st filer", "nodes", availableNodes)
						availableNodes = filterNodesWithCapacity(ctx, lc, log, availableNodes, resVolumesSizeKb)
						log.Info("Node by res name after 2nd filer", "nodes", availableNodes)
						// TODO: add another filter to dron nodes from linstorNodes
						if int32(len(availableNodes)) < desiredRelicaCount {
							log.Info("debug info:", "available node", len(availableNodes), "desired node", desiredRelicaCount)
							log.Error(errors.New("not enough available nodes"), "check nodes count")
							break
						}

						// TODO: How get current replica count and modify it?
						g := new(errgroup.Group)
						for i := int32(0); i < desiredRelicaCount; i++ {
							g.Go(func() error {
								resCreate := lclient.ResourceCreate{
									Resource: lclient.Resource{
										Name:     res.Name,
										NodeName: availableNodes[i],
									},
								}
								return lc.Resources.Create(ctx, resCreate)
							})
							// TODO: in some cases add
							//g.Go(func() error {
							//	return lc.Resources.Diskless(ctx, res.Name, availableNodes[i], DisklessStoragePool)
							//})
						}
						if err := g.Wait(); err == nil {
							log.Info("successful add resources")
						} else {
							log.Error(err, "error while creating resources on nodes")
						}

						// TODO: add retries?

						// Check replicas count after add new ones
						resGroup, err = lc.ResourceGroups.Get(ctx, resDefinition.ResourceGroupName)
						if err != nil {
							log.Error(err, "could not get resource group from controller", resDefinition.ResourceGroupName)
							break
						}
						if desiredRelicaCount != resGroup.SelectFilter.PlaceCount {
							log.Error(errors.New("Warning! Create not enough replicas of resource"), "error creating")
							nodeToBeDeleted = false
						} else {
							nodeToBeDeleted = true
						}
					}
				}

				if nodeToBeDeleted {
					err = lc.Nodes.Delete(ctx, drbdNodeToRemove.Name)
					if err != nil {
						log.Error(err, "unable to remove LINSTOR node: "+drbdNodeToRemove.Name)
					}
					break
				}
			}
		}

		//if labels.Set(drbdNodeSelector).AsSelector().Matches(labels.Set(drbdNodeToRemove.Labels)) {
		//	log.Info("Kubernetes node: " + drbdNodeToRemove.Name + "  have drbd label. Unset it")
		//	log.Error(nil, "Warning! Delete logic not yet implemented. Removal of LINSTOR nodes is prohibited.")
		//
		//	// TODO: now it should be uncomment?
		//	originalNode := drbdNodeToRemove.DeepCopy()
		//	newNode := drbdNodeToRemove.DeepCopy()
		//	for labelKey := range drbdNodeSelector {
		//		delete(newNode.Labels, labelKey)
		//	}
		//
		//	err := cl.Patch(ctx, newNode, client.MergeFrom(originalNode))
		//	if err != nil {
		//		log.Error(err, "Unable unset drbd labels from node %s. "+drbdNodeToRemove.Name)
		//	}
		//}

	}
	return nil

}

func AddOrConfigureDRBDNodes(ctx context.Context, cl client.Client, lc *lclient.Client, log logr.Logger, selectedKubernetesNodes v1.NodeList, linstorNodes []lclient.Node, drbdStorageClasses sdsapi.DRBDStorageClassList, drbdNodeSelector map[string]string) error {

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

		err := ReconcileKubernetesNodeLabels(ctx, cl, log, selectedKubernetesNode, drbdStorageClasses, drbdNodeSelector, true)
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

func ConfigureDRBDNode(ctx context.Context, lc *lclient.Client, linstorNode lclient.Node, drbdNodeProperties map[string]string) error {
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

func CreateDRBDNode(ctx context.Context, lc *lclient.Client, selectedKubernetesNode v1.Node, drbdNodeProperties map[string]string) error {
	newLinstorNode := lclient.Node{
		Name: selectedKubernetesNode.Name,
		Type: LinstorSatelliteType,
		NetInterfaces: []lclient.NetInterface{
			{
				Name:                    "default",
				Address:                 net.ParseIP(selectedKubernetesNode.Status.Addresses[0].Address),
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

	for k, v := range kubernetesNodeLabels {
		properties[fmt.Sprintf("Aux/%s", k)] = v
	}

	return properties
}

func GetKubernetesSecretByName(ctx context.Context, cl client.Client, secretName string, secretNamespace string) (*v1.Secret, error) {
	secret := &v1.Secret{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      secretName,
		Namespace: secretNamespace,
	}, secret)
	return secret, err
}

func GetKubernetesNodesBySelector(ctx context.Context, cl client.Client, nodeSelector map[string]string) (v1.NodeList, error) {
	selectedK8sNodes := v1.NodeList{}
	err := cl.List(ctx, &selectedK8sNodes, client.MatchingLabels(nodeSelector))
	return selectedK8sNodes, err
}

func GetAllKubernetesNodes(ctx context.Context, cl client.Client) (v1.NodeList, error) {
	allKubernetesNodes := v1.NodeList{}
	err := cl.List(ctx, &allKubernetesNodes)
	return allKubernetesNodes, err
}

func GetNodeSelectorFromConfig(secret v1.Secret) (map[string]string, error) {
	var secretConfig sdsapi.SdsDRBDOperatorConfig
	err := yaml.Unmarshal(secret.Data["config"], &secretConfig)
	if err != nil {
		return nil, err
	}
	nodeSelector := secretConfig.NodeSelector
	return nodeSelector, err
}

func DiffNodeLists(leftList, rightList v1.NodeList) v1.NodeList {
	var diff v1.NodeList

	for _, leftNode := range leftList.Items {
		if !ContainsNode(rightList, leftNode) {
			diff.Items = append(diff.Items, leftNode)
		}
	}
	return diff
}

func ContainsNode(nodeList v1.NodeList, node v1.Node) bool {
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

	linstorControllerNodes := []lclient.Node{}
	linstorSatelliteNodes := []lclient.Node{}

	for _, linstorNode := range linstorNodes {
		if linstorNode.Type == LinstorControllerType {
			linstorControllerNodes = append(linstorControllerNodes, linstorNode)
		} else if linstorNode.Type == LinstorSatelliteType {
			linstorSatelliteNodes = append(linstorSatelliteNodes, linstorNode)
		}
	}

	return linstorSatelliteNodes, linstorControllerNodes, nil
}

func removeLinstorControllerNodes(ctx context.Context, lc *lclient.Client, log logr.Logger, linstorControllerNodes []lclient.Node) error {
	for _, linstorControllerNode := range linstorControllerNodes {
		log.Info("removeLinstorControllerNodes: Remove LINSTOR controller node: " + linstorControllerNode.Name)
		err := lc.Nodes.Delete(ctx, linstorControllerNode.Name)
		if err != nil {
			return err
		}
	}
	return nil
}

func ReconcileKubernetesNodeLabels(ctx context.Context, cl client.Client, log logr.Logger, kubernetesNode v1.Node, drbdStorageClasses sdsapi.DRBDStorageClassList, drbdNodeSelector map[string]string, isDRBDNode bool) error {
	labelsToAdd := make(map[string]string)
	labelsToRemove := make(map[string]string)
	storageClassesLabelsForNode := make(map[string]string)

	if isDRBDNode {
		if !labels.Set(drbdNodeSelector).AsSelector().Matches(labels.Set(kubernetesNode.Labels)) {
			log.Info(fmt.Sprintf("Kubernetes node '%s' has not drbd label. Set it.", kubernetesNode.Name))
			labelsToAdd = labels.Merge(labelsToAdd, drbdNodeSelector)
		}

		storageClassesLabelsForNode = GetStorageClassesLabelsForNode(kubernetesNode, drbdStorageClasses)
		for labelKey, labelValue := range storageClassesLabelsForNode {
			if _, existsInKubernetesNodeLabels := kubernetesNode.Labels[labelKey]; !existsInKubernetesNodeLabels {
				labelsToAdd[labelKey] = labelValue
			}
		}
	} else {
		if labels.Set(drbdNodeSelector).AsSelector().Matches(labels.Set(kubernetesNode.Labels)) {
			log.Info(fmt.Sprintf("Kubernetes node: '%s' has a DRBD label but is no longer a DRBD node. Removing DRBD label.", kubernetesNode.Name))
			log.Error(nil, "Warning! Delete logic not yet implemented. Removal of DRBD label is prohibited.")
		}
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

func GetStorageClassesLabelsForNode(kubernetesNode v1.Node, drbdStorageClasses sdsapi.DRBDStorageClassList) map[string]string {
	storageClassesLabels := make(map[string]string)

	for _, drbdStorageClass := range drbdStorageClasses.Items {
		if drbdStorageClass.Spec.Zones == nil {
			continue
		}
		for _, zone := range drbdStorageClass.Spec.Zones {
			if zone == kubernetesNode.Labels[ZoneLabel] {
				storageClassLabelKey := fmt.Sprintf("%s/%s", StorageClassLabelKeyPrefix, drbdStorageClass.Name)
				storageClassesLabels = labels.Merge(storageClassesLabels, map[string]string{storageClassLabelKey: ""})
				break
			}
		}
	}
	return storageClassesLabels
}

func getAvailableNodes(allNodes []lclient.Node, storageNode []string) []string {
	result := make([]string, 0, len(allNodes))
	for _, node := range allNodes {
		founded := false
		for _, sn := range storageNode {
			if node.Name == sn {
				founded = true
				break
			}
		}
		if !founded {
			result = append(result, node.Name)
		}
	}
	return result
}

func getNodesByResourceName(ctx context.Context, lc *lclient.Client, resName string) ([]string, error) {
	nodes := make([]string, 0, 10)
	resources, err := lc.Resources.GetAll(ctx, resName)
	if err != nil {
		return nodes, err
	}
	for _, res := range resources {
		// Maybe excess condition
		if res.Name == resName {
			nodes = append(nodes, res.NodeName)
		}
	}
	return nodes, nil
}

func filterNodesWithCapacity(ctx context.Context, lc *lclient.Client, nodes []string, size uint64) []string {
	result := make([]string, 0, len(nodes))
	for _, node := range nodes {
		storagePools, err := lc.Nodes.GetStoragePools(ctx, node)
		if err != nil {
			return []string{}
		} else {
			for _, sp := range storagePools {
				// Here check for "DfltDisklessStorPool" is important
				if sp.StoragePoolName != DisklessStoragePool && size <= uint64(sp.FreeCapacity) {
					result = append(result, node)
					break
				}
			}
		}
	}
	return result
}
