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
	"bytes"
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	lapi "github.com/LINBIT/golinstor/client"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"sds-replicated-volume-controller/pkg/logger"
)

const (
	ReplicatedStoragePoolControllerName = "replicated-storage-pool-controller"
	TypeLVMThin                         = "LVMThin"
	TypeLVM                             = "LVM"
	LVMVGTypeLocal                      = "Local"
	StorPoolNamePropKey                 = "StorDriver/StorPoolName"
)

func NewReplicatedStoragePool(
	mgr manager.Manager,
	lc *lapi.Client,
	interval int,
	log logger.Logger,
) (controller.Controller, error) {
	cl := mgr.GetClient()

	c, err := controller.New(ReplicatedStoragePoolControllerName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			log.Info("START from reconciler reconcile of replicated storage pool with name: " + request.Name)

			shouldRequeue, err := ReconcileReplicatedStoragePoolEvent(ctx, cl, request, log, lc)
			if shouldRequeue {
				log.Error(err, fmt.Sprintf("error in ReconcileReplicatedStoragePoolEvent. Add to retry after %d seconds.", interval))
				return reconcile.Result{
					RequeueAfter: time.Duration(interval) * time.Second,
				}, nil
			}

			log.Info("END from reconciler reconcile of replicated storage pool with name: " + request.Name)
			return reconcile.Result{}, nil
		}),
	})

	if err != nil {
		return nil, err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &srv.ReplicatedStoragePool{}, handler.TypedFuncs[*srv.ReplicatedStoragePool, reconcile.Request]{
		CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[*srv.ReplicatedStoragePool], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Info("START from CREATE reconcile of Replicated storage pool with name: " + e.Object.GetName())

			request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
			shouldRequeue, err := ReconcileReplicatedStoragePoolEvent(ctx, cl, request, log, lc)
			if shouldRequeue {
				log.Error(err, fmt.Sprintf("error in ReconcileReplicatedStoragePoolEvent. Add to retry after %d seconds.", interval))
				q.AddAfter(request, time.Duration(interval)*time.Second)
			}

			log.Info("END from CREATE reconcile of Replicated storage pool with name: " + request.Name)
		},
		UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[*srv.ReplicatedStoragePool], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Info("START from UPDATE reconcile of Replicated storage pool with name: " + e.ObjectNew.GetName())

			if reflect.DeepEqual(e.ObjectOld.Spec, e.ObjectNew.Spec) {
				log.Debug("StoragePool spec not changed. Nothing to do")
				log.Info("END from UPDATE reconcile of Replicated storage pool with name: " + e.ObjectNew.GetName())
				return
			}

			if e.ObjectOld.Spec.Type != e.ObjectNew.Spec.Type {
				errMessage := fmt.Sprintf("StoragePool spec changed. Type change is forbidden. Old type: %s, new type: %s", e.ObjectOld.Spec.Type, e.ObjectNew.Spec.Type)
				log.Error(nil, errMessage)
				e.ObjectNew.Status.Phase = "Failed"
				e.ObjectNew.Status.Reason = errMessage
				err := UpdateReplicatedStoragePool(ctx, cl, e.ObjectNew)
				if err != nil {
					log.Error(err, "error UpdateReplicatedStoragePool")
				}
				return
			}

			config, err := rest.InClusterConfig()
			if err != nil {
				klog.Fatal(err.Error())
			}

			staticClient, err := kubernetes.NewForConfig(config)
			if err != nil {
				klog.Fatal(err)
			}

			var ephemeralNodesList []string

			nodes, _ := staticClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node.deckhouse.io/type=CloudEphemeral"})
			for _, node := range nodes.Items {
				ephemeralNodesList = append(ephemeralNodesList, node.Name)
			}

			listDevice := &snc.LVMVolumeGroupList{}

			err = cl.List(ctx, listDevice)
			if err != nil {
				log.Error(err, "Error while getting LVM Volume Groups list")
				return
			}

			for _, lvmVolumeGroup := range e.ObjectNew.Spec.LVMVolumeGroups {
				for _, lvg := range listDevice.Items {
					if lvg.Name != lvmVolumeGroup.Name {
						continue
					}
					for _, lvgNode := range lvg.Status.Nodes {
						if slices.Contains(ephemeralNodesList, lvgNode.Name) {
							errMessage := fmt.Sprintf("Cannot create storage pool on ephemeral node (%s)", lvgNode.Name)
							log.Error(nil, errMessage)
							e.ObjectNew.Status.Phase = "Failed"
							e.ObjectNew.Status.Reason = errMessage
							err = UpdateReplicatedStoragePool(ctx, cl, e.ObjectNew)
							if err != nil {
								log.Error(err, "error UpdateReplicatedStoragePool")
							}
							return
						}
					}
				}
			}

			request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.ObjectNew.GetNamespace(), Name: e.ObjectNew.GetName()}}
			shouldRequeue, err := ReconcileReplicatedStoragePoolEvent(ctx, cl, request, log, lc)
			if shouldRequeue {
				log.Error(err, fmt.Sprintf("error in ReconcileReplicatedStoragePoolEvent. Add to retry after %d seconds.", interval))
				q.AddAfter(request, time.Duration(interval)*time.Second)
			}

			log.Info("END from UPDATE reconcile of Replicated storage pool with name: " + request.Name)
		},
	}))

	return c, err
}

func ReconcileReplicatedStoragePoolEvent(ctx context.Context, cl client.Client, request reconcile.Request, log logger.Logger, lc *lapi.Client) (bool, error) {
	replicatedSP := &srv.ReplicatedStoragePool{}
	err := cl.Get(ctx, request.NamespacedName, replicatedSP)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Warning("StoragePool with name: " + request.Name + " not found. Object was probably deleted. Remove it from quie as deletion logic not implemented yet.")
			return false, nil
		}
		return true, fmt.Errorf("error getting StoragePool: %s", err.Error())
	}
	err = ReconcileReplicatedStoragePool(ctx, cl, lc, log, replicatedSP)
	if err != nil {
		return true, fmt.Errorf("error ReconcileReplicatedStoragePool: %s", err.Error())
	}
	return false, nil
}

func ReconcileReplicatedStoragePool(ctx context.Context, cl client.Client, lc *lapi.Client, log logger.Logger, replicatedSP *srv.ReplicatedStoragePool) error { // TODO: add shouldRequeue as returned value
	ok, msg, lvmVolumeGroups := GetAndValidateVolumeGroups(ctx, cl, replicatedSP.Spec.Type, replicatedSP.Spec.LVMVolumeGroups)
	if !ok {
		replicatedSP.Status.Phase = "Failed"
		replicatedSP.Status.Reason = msg
		err := UpdateReplicatedStoragePool(ctx, cl, replicatedSP)
		if err != nil {
			return fmt.Errorf("error UpdateReplicatedStoragePool: %s", err.Error())
		}
		return fmt.Errorf("unable to reconcile the Replicated Storage Pool %s, reason: %s", replicatedSP.Name, msg)
	}
	var (
		lvmVgForLinstor  string
		lvmType          lapi.ProviderKind
		failedMsgBuilder strings.Builder
		isSuccessful     = true
	)

	failedMsgBuilder.WriteString("Error occurred while creating Storage Pools: ")

	for _, replicatedSPLVMVolumeGroup := range replicatedSP.Spec.LVMVolumeGroups {
		lvmVolumeGroup, ok := lvmVolumeGroups[replicatedSPLVMVolumeGroup.Name]
		nodeName := lvmVolumeGroup.Status.Nodes[0].Name

		if !ok {
			log.Error(nil, fmt.Sprintf("Error getting LVMVolumeGroup %s from LVMVolumeGroups map: %+v", replicatedSPLVMVolumeGroup.Name, lvmVolumeGroups))
			failedMsgBuilder.WriteString(fmt.Sprintf("Error getting LVMVolumeGroup %s from LVMVolumeGroups map. See logs of %s for details; ", replicatedSPLVMVolumeGroup.Name, ReplicatedStoragePoolControllerName))
			isSuccessful = false
			continue
		}

		switch replicatedSP.Spec.Type {
		case TypeLVM:
			lvmType = lapi.LVM
			lvmVgForLinstor = lvmVolumeGroup.Spec.ActualVGNameOnTheNode
		case TypeLVMThin:
			lvmType = lapi.LVM_THIN
			lvmVgForLinstor = lvmVolumeGroup.Spec.ActualVGNameOnTheNode + "/" + replicatedSPLVMVolumeGroup.ThinPoolName
		}

		newStoragePool := lapi.StoragePool{
			StoragePoolName: replicatedSP.Name,
			NodeName:        nodeName,
			ProviderKind:    lvmType,
			Props: map[string]string{
				StorPoolNamePropKey: lvmVgForLinstor,
			},
		}

		existedStoragePool, err := lc.Nodes.GetStoragePool(ctx, nodeName, replicatedSP.Name)
		if err != nil {
			if err == lapi.NotFoundError {
				log.Info(fmt.Sprintf("[ReconcileReplicatedStoragePool] Storage Pool %s on node %s on vg %s was not found. Creating it", replicatedSP.Name, nodeName, lvmVgForLinstor))
				createErr := lc.Nodes.CreateStoragePool(ctx, nodeName, newStoragePool)
				if createErr != nil {
					log.Error(createErr, fmt.Sprintf("[ReconcileReplicatedStoragePool] unable to create Linstor Storage Pool %s on the node %s in the VG %s", newStoragePool.StoragePoolName, nodeName, lvmVgForLinstor))

					log.Info(fmt.Sprintf("[ReconcileReplicatedStoragePool] Try to delete Storage Pool %s on the node %s in the VG %s from LINSTOR if it was mistakenly created", newStoragePool.StoragePoolName, nodeName, lvmVgForLinstor))
					delErr := lc.Nodes.DeleteStoragePool(ctx, nodeName, replicatedSP.Name)
					if delErr != nil {
						log.Error(delErr, fmt.Sprintf("[ReconcileReplicatedStoragePool] unable to delete LINSTOR Storage Pool %s on node %s in the VG %s", replicatedSP.Name, nodeName, lvmVgForLinstor))
					}

					replicatedSP.Status.Phase = "Failed"
					replicatedSP.Status.Reason = createErr.Error()
					updErr := UpdateReplicatedStoragePool(ctx, cl, replicatedSP)
					if updErr != nil {
						log.Error(updErr, fmt.Sprintf("[ReconcileReplicatedStoragePool] unable to update the Replicated Storage Pool %s", replicatedSP.Name))
					}
					return createErr
				}

				log.Info(fmt.Sprintf("Storage Pool %s was successfully created on the node %s in the VG %s", replicatedSP.Name, nodeName, lvmVgForLinstor))
				continue
			}
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStoragePool] unable to get the Linstor Storage Pool %s on the node %s in the VG %s", replicatedSP.Name, nodeName, lvmVgForLinstor))

			failedMsgBuilder.WriteString(err.Error())
			isSuccessful = false
			continue
		}

		log.Info(fmt.Sprintf("[ReconcileReplicatedStoragePool] the Linstor Storage Pool %s on node %s on vg %s already exists. Check it", replicatedSP.Name, nodeName, lvmVgForLinstor))

		if existedStoragePool.ProviderKind != newStoragePool.ProviderKind {
			errMessage := fmt.Sprintf("Storage Pool %s on node %s on vg %s already exists but with different type %s. New type is %s. Type change is forbidden; ", replicatedSP.Name, nodeName, lvmVgForLinstor, existedStoragePool.ProviderKind, newStoragePool.ProviderKind)
			log.Error(nil, errMessage)
			failedMsgBuilder.WriteString(errMessage)
			isSuccessful = false
		}

		if existedStoragePool.Props[StorPoolNamePropKey] != lvmVgForLinstor {
			errMessage := fmt.Sprintf("Storage Pool %s on node %s already exists with vg \"%s\". New vg is \"%s\". VG change is forbidden; ", replicatedSP.Name, nodeName, existedStoragePool.Props[StorPoolNamePropKey], lvmVgForLinstor)
			log.Error(nil, errMessage)
			failedMsgBuilder.WriteString(errMessage)
			isSuccessful = false
		}
	}

	if !isSuccessful {
		replicatedSP.Status.Phase = "Failed"
		replicatedSP.Status.Reason = failedMsgBuilder.String()
		err := UpdateReplicatedStoragePool(ctx, cl, replicatedSP)
		if err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStoragePool] unable to update the Replicated Storage Pool %s", replicatedSP.Name))
			return err
		}
		return fmt.Errorf("some errors have been occurred while creating Storage Pool %s, err: %s", replicatedSP.Name, failedMsgBuilder.String())
	}

	replicatedSP.Status.Phase = "Completed"
	replicatedSP.Status.Reason = "pool creation completed"
	err := UpdateReplicatedStoragePool(ctx, cl, replicatedSP)
	if err != nil {
		log.Error(err, fmt.Sprintf("[ReconcileReplicatedStoragePool] unable to update the Replicated Storage Pool %s", replicatedSP.Name))
		return err
	}

	return nil
}

func UpdateReplicatedStoragePool(ctx context.Context, cl client.Client, replicatedSP *srv.ReplicatedStoragePool) error {
	err := cl.Update(ctx, replicatedSP)
	if err != nil {
		return err
	}
	return nil
}

func GetReplicatedStoragePool(ctx context.Context, cl client.Client, namespace, name string) (*srv.ReplicatedStoragePool, error) {
	obj := &srv.ReplicatedStoragePool{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, obj)
	if err != nil {
		return nil, err
	}
	return obj, err
}

func GetLVMVolumeGroup(ctx context.Context, cl client.Client, name string) (*snc.LVMVolumeGroup, error) {
	obj := &snc.LVMVolumeGroup{}
	err := cl.Get(ctx, client.ObjectKey{
		Name: name,
	}, obj)
	return obj, err
}

func GetAndValidateVolumeGroups(ctx context.Context, cl client.Client, lvmType string, replicatedSPLVMVolumeGroups []srv.ReplicatedStoragePoolLVMVolumeGroups) (bool, string, map[string]snc.LVMVolumeGroup) {
	var lvmVolumeGroupName string
	var nodeName string
	nodesWithlvmVolumeGroups := make(map[string]string)
	invalidLVMVolumeGroups := make(map[string]string)
	lvmVolumeGroupsNames := make(map[string]bool)
	lvmVolumeGroups := make(map[string]snc.LVMVolumeGroup)

	for _, g := range replicatedSPLVMVolumeGroups {
		lvmVolumeGroupName = g.Name

		if lvmVolumeGroupsNames[lvmVolumeGroupName] {
			invalidLVMVolumeGroups[lvmVolumeGroupName] = "LVMVolumeGroup name is not unique"
			continue
		}
		lvmVolumeGroupsNames[lvmVolumeGroupName] = true

		lvmVolumeGroup, err := GetLVMVolumeGroup(ctx, cl, lvmVolumeGroupName)
		if err != nil {
			UpdateMapValue(invalidLVMVolumeGroups, lvmVolumeGroupName, fmt.Sprintf("Error getting LVMVolumeGroup: %s", err.Error()))
			continue
		}

		if lvmVolumeGroup.Spec.Type != LVMVGTypeLocal {
			UpdateMapValue(invalidLVMVolumeGroups, lvmVolumeGroupName, fmt.Sprintf("LVMVolumeGroup type is not %s", LVMVGTypeLocal))
			continue
		}

		if len(lvmVolumeGroup.Status.Nodes) != 1 {
			UpdateMapValue(invalidLVMVolumeGroups, lvmVolumeGroupName, "LVMVolumeGroup has more than one node in status.nodes. LVMVolumeGroup for LINSTOR Storage Pool must to have only one node")
			continue
		}

		nodeName = lvmVolumeGroup.Status.Nodes[0].Name
		if value, ok := nodesWithlvmVolumeGroups[nodeName]; ok {
			UpdateMapValue(invalidLVMVolumeGroups, lvmVolumeGroupName, fmt.Sprintf("This LVMVolumeGroup have same node %s as LVMVolumeGroup with name: %s. LINSTOR Storage Pool is allowed to have only one LVMVolumeGroup per node", nodeName, value))
		}

		switch lvmType {
		case TypeLVMThin:
			if len(g.ThinPoolName) == 0 {
				UpdateMapValue(invalidLVMVolumeGroups, lvmVolumeGroupName, fmt.Sprintf("type %s but ThinPoolName is not set", TypeLVMThin))
				break
			}
			found := false
			for _, thinPool := range lvmVolumeGroup.Spec.ThinPools {
				if g.ThinPoolName == thinPool.Name {
					found = true
					break
				}
			}
			if !found {
				UpdateMapValue(invalidLVMVolumeGroups, lvmVolumeGroupName, fmt.Sprintf("ThinPoolName %s is not found in Spec.ThinPools of LVMVolumeGroup %s", g.ThinPoolName, lvmVolumeGroupName))
			}
		case TypeLVM:
			if len(g.ThinPoolName) != 0 {
				UpdateMapValue(invalidLVMVolumeGroups, lvmVolumeGroupName, fmt.Sprintf("type %s but ThinPoolName is set", TypeLVM))
			}
		}

		nodesWithlvmVolumeGroups[nodeName] = lvmVolumeGroupName
		lvmVolumeGroups[lvmVolumeGroupName] = *lvmVolumeGroup
	}

	if len(invalidLVMVolumeGroups) > 0 {
		msg := GetOrderedMapValuesAsString(invalidLVMVolumeGroups)
		return false, msg, nil
	}

	return true, "", lvmVolumeGroups
}

func UpdateMapValue(m map[string]string, key string, additionalValue string) {
	if oldValue, ok := m[key]; ok {
		m[key] = fmt.Sprintf("%s. Also: %s", oldValue, additionalValue)
	} else {
		m[key] = additionalValue
	}
}

func GetOrderedMapValuesAsString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k) // TODO: change append
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, k := range keys {
		v := m[k]
		fmt.Fprintf(&buf, "%s: %s\n", k, v)
	}
	return buf.String()
}
