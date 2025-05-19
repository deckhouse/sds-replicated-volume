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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/sds-replicated-volume-controller/config"
	"github.com/deckhouse/sds-replicated-volume/images/sds-replicated-volume-controller/pkg/logger"
)

const (
	ReplicatedStorageClassControllerName = "replicated-storage-class-controller"
	// TODO
	ReplicatedStorageClassFinalizerName = "replicatedstorageclass.storage.deckhouse.io"
	// TODO
	StorageClassFinalizerName = "storage.deckhouse.io/sds-replicated-volume"
	StorageClassProvisioner   = "replicated.csi.storage.deckhouse.io"
	StorageClassKind          = "StorageClass"
	StorageClassAPIVersion    = "storage.k8s.io/v1"

	ZoneLabel                  = "topology.kubernetes.io/zone"
	StorageClassLabelKeyPrefix = "class.storage.deckhouse.io"

	VolumeAccessLocal           = "Local"
	VolumeAccessEventuallyLocal = "EventuallyLocal"
	VolumeAccessPreferablyLocal = "PreferablyLocal"
	VolumeAccessAny             = "Any"

	ReclaimPolicyRetain = "Retain"
	ReclaimPolicyDelete = "Delete"

	ReplicationNone                       = "None"
	ReplicationAvailability               = "Availability"
	ReplicationConsistencyAndAvailability = "ConsistencyAndAvailability"

	TopologyTransZonal = "TransZonal"
	TopologyZonal      = "Zonal"
	TopologyIgnored    = "Ignored"

	StorageClassPlacementCountKey                 = "replicated.csi.storage.deckhouse.io/placementCount"
	StorageClassAutoEvictMinReplicaCountKey       = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/AutoEvictMinReplicaCount"
	StorageClassStoragePoolKey                    = "replicated.csi.storage.deckhouse.io/storagePool"
	StorageClassParamReplicasOnDifferentKey       = "replicated.csi.storage.deckhouse.io/replicasOnDifferent"
	StorageClassParamReplicasOnSameKey            = "replicated.csi.storage.deckhouse.io/replicasOnSame"
	StorageClassParamAllowRemoteVolumeAccessKey   = "replicated.csi.storage.deckhouse.io/allowRemoteVolumeAccess"
	StorageClassParamAllowRemoteVolumeAccessValue = "- fromSame:\n  - topology.kubernetes.io/zone"
	ReplicatedStorageClassParamNameKey            = "replicated.csi.storage.deckhouse.io/replicatedStorageClassName"
	StorageClassLVMVolumeGroupsParamKey           = "replicated.csi.storage.deckhouse.io/lvm-volume-groups"
	StorageClassLVMType                           = "csi.storage.deckhouse.io/lvm-type"

	StorageClassParamFSTypeKey                     = "csi.storage.k8s.io/fstype"
	FsTypeExt4                                     = "ext4"
	StorageClassParamPlacementPolicyKey            = "replicated.csi.storage.deckhouse.io/placementPolicy"
	PlacementPolicyAutoPlaceTopology               = "AutoPlaceTopology"
	StorageClassParamNetProtocolKey                = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Net/protocol"
	NetProtocolC                                   = "C"
	StorageClassParamNetRRConflictKey              = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Net/rr-conflict"
	RrConflictRetryConnect                         = "retry-connect"
	StorageClassParamAutoQuorumKey                 = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-quorum"
	SuspendIo                                      = "suspend-io"
	StorageClassParamAutoAddQuorumTieBreakerKey    = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-add-quorum-tiebreaker"
	StorageClassParamOnNoQuorumKey                 = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-no-quorum"
	StorageClassParamOnNoDataAccessibleKey         = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-no-data-accessible"
	StorageClassParamOnSuspendedPrimaryOutdatedKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-suspended-primary-outdated"
	PrimaryOutdatedForceSecondary                  = "force-secondary"

	StorageClassParamAutoDiskfulKey             = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-diskful"
	StorageClassParamAutoDiskfulAllowCleanupKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-diskful-allow-cleanup"

	ManagedLabelKey   = "storage.deckhouse.io/managed-by"
	ManagedLabelValue = "sds-replicated-volume"

	Created = "Created"
	Failed  = "Failed"

	DefaultStorageClassAnnotationKey = "storageclass.kubernetes.io/is-default-class"
)

func NewReplicatedStorageClass(
	mgr manager.Manager,
	cfg *config.Options,
	log logger.Logger,
) (controller.Controller, error) {
	cl := mgr.GetClient()

	c, err := controller.New(ReplicatedStorageClassControllerName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			log.Info(fmt.Sprintf("[ReplicatedStorageClassReconciler] Get event for ReplicatedStorageClass %s in reconciler", request.Name))
			shouldRequeue, err := ReconcileReplicatedStorageClassEvent(ctx, cl, log, cfg, request)
			if err != nil {
				log.Error(err, "[ReplicatedStorageClassReconciler] error in ReconcileReplicatedStorageClassEvent")
			}
			if shouldRequeue {
				log.Warning(fmt.Sprintf("[ReplicatedStorageClassReconciler] ReconcileReplicatedStorageClassEvent should be reconciled again. Add to retry after %d seconds.", cfg.ScanInterval))
				return reconcile.Result{Requeue: true, RequeueAfter: time.Duration(cfg.ScanInterval) * time.Second}, nil
			}

			log.Info(fmt.Sprintf("[ReplicatedStorageClassReconciler] Finish event for ReplicatedStorageClass %s in reconciler. No need to reconcile it again. ", request.Name))
			return reconcile.Result{}, nil
		}),
	})

	if err != nil {
		return nil, err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &srv.ReplicatedStorageClass{}, handler.TypedFuncs[*srv.ReplicatedStorageClass, reconcile.Request]{
		CreateFunc: func(_ context.Context, e event.TypedCreateEvent[*srv.ReplicatedStorageClass], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Debug(fmt.Sprintf("[ReplicatedStorageClassReconciler] Get CREATE event for ReplicatedStorageClass %s. Add it to queue.", e.Object.GetName()))
			request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
			q.Add(request)
		},
		UpdateFunc: func(_ context.Context, e event.TypedUpdateEvent[*srv.ReplicatedStorageClass], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Debug(fmt.Sprintf("[ReplicatedStorageClassReconciler] Get UPDATE event for ReplicatedStorageClass %s. Check if it was changed.", e.ObjectNew.GetName()))
			log.Trace(fmt.Sprintf("[ReplicatedStorageClassReconciler] Old ReplicatedStorageClass: %+v", e.ObjectOld))
			log.Trace(fmt.Sprintf("[ReplicatedStorageClassReconciler] New ReplicatedStorageClass: %+v", e.ObjectNew))
			if e.ObjectNew.GetDeletionTimestamp() != nil || !reflect.DeepEqual(e.ObjectNew.Spec, e.ObjectOld.Spec) {
				log.Debug(fmt.Sprintf("[ReplicatedStorageClassReconciler] ReplicatedStorageClass %s was changed. Add it to queue.", e.ObjectNew.GetName()))
				request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.ObjectNew.GetNamespace(), Name: e.ObjectNew.GetName()}}
				q.Add(request)
			}
		},
	}))
	if err != nil {
		return nil, err
	}
	return c, err
}

func ReconcileReplicatedStorageClassEvent(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	cfg *config.Options,
	request reconcile.Request,
) (bool, error) {
	log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClassEvent] Try to get ReplicatedStorageClass with name: %s",
		request.Name))

	replicatedSC, err := GetReplicatedStorageClass(ctx, cl, request.Namespace, request.Name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info(fmt.Sprintf("[ReconcileReplicatedStorageClassEvent] "+
				"ReplicatedStorageClass with name: %s not found. Finish reconcile.", request.Name))
			return false, nil
		}

		return true, fmt.Errorf("error getting ReplicatedStorageClass: %w", err)
	}

	sc, err := GetStorageClass(ctx, cl, replicatedSC.Name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("[ReconcileReplicatedStorageClassEvent] StorageClass with name: " +
				replicatedSC.Name + " not found.")
		} else {
			return true, fmt.Errorf("error getting StorageClass: %w", err)
		}
	}

	if sc != nil && sc.Provisioner != StorageClassProvisioner {
		return false, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] Reconcile StorageClass with provisioner %s is not allowed", sc.Provisioner)
	}

	// Handle deletion
	if replicatedSC.ObjectMeta.DeletionTimestamp != nil {
		log.Info("[ReconcileReplicatedStorageClass] ReplicatedStorageClass with name: " +
			replicatedSC.Name + " is marked for deletion. Removing it.")
		shouldRequeue, err := ReconcileDeleteReplicatedStorageClass(ctx, cl, log, replicatedSC, sc)
		if err != nil {
			if updateErr := updateReplicatedStorageClassStatus(ctx, cl, log, replicatedSC, Failed, err.Error()); updateErr != nil {
				err = errors.Join(err, updateErr)
				err = fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error after "+
					"ReconcileDeleteReplicatedStorageClass and error after UpdateReplicatedStorageClass: %w", err)
				shouldRequeue = true
			}
		}
		return shouldRequeue, err
	}

	// Normal reconciliation
	shouldRequeue, err := ReconcileReplicatedStorageClass(ctx, cl, log, cfg, replicatedSC, sc)
	if err != nil {
		if updateErr := updateReplicatedStorageClassStatus(ctx, cl, log, replicatedSC, Failed, err.Error()); updateErr != nil {
			err = errors.Join(err, updateErr)
			err = fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error after ReconcileReplicatedStorageClass"+
				"and error after UpdateReplicatedStorageClass: %w", err)
			shouldRequeue = true
		}
	}

	return shouldRequeue, err
}

func ReconcileReplicatedStorageClass(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	cfg *config.Options,
	replicatedSC *srv.ReplicatedStorageClass,
	oldSC *storagev1.StorageClass,
) (bool, error) {
	log.Info("[ReconcileReplicatedStorageClass] Validating ReplicatedStorageClass with name: " + replicatedSC.Name)

	zones, err := GetClusterZones(ctx, cl)
	if err != nil {
		err = fmt.Errorf("[ReconcileReplicatedStorageClass] error GetClusterZones: %w", err)
		return true, err
	}

	valid, msg := ValidateReplicatedStorageClass(replicatedSC, zones)
	if !valid {
		err := fmt.Errorf("[ReconcileReplicatedStorageClass] Validation of "+
			"ReplicatedStorageClass %s failed for the following reason: %s", replicatedSC.Name, msg)
		return false, err
	}
	log.Info("[ReconcileReplicatedStorageClass] ReplicatedStorageClass with name: " +
		replicatedSC.Name + " is valid")

	log.Trace("[ReconcileReplicatedStorageClass] Check if virtualization module is enabled and if " +
		"the ReplicatedStorageClass has VolumeAccess set to Local")
	var virtualizationEnabled bool
	if replicatedSC.Spec.VolumeAccess == VolumeAccessLocal {
		virtualizationEnabled, err = GetVirtualizationModuleEnabled(ctx, cl, log,
			types.NamespacedName{Name: ControllerConfigMapName, Namespace: cfg.ControllerNamespace})
		if err != nil {
			err = fmt.Errorf("[ReconcileReplicatedStorageClass] error GetVirtualizationModuleEnabled: %w", err)
			return true, err
		}
		log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClass] ReplicatedStorageClass has VolumeAccess set "+
			"to Local and virtualization module is %t", virtualizationEnabled))
	}

	rspData, err := GetReplicatedStoragePoolData(ctx, cl, replicatedSC)
	if err != nil {
		err = fmt.Errorf("[ReconcileReplicatedStorageClass] error getting replicated storage class'es LVGs: %w", err)
		return false, err
	}

	newSC := GetNewStorageClass(replicatedSC, virtualizationEnabled, rspData)

	if oldSC == nil {
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " +
			replicatedSC.Name + " not found. Create it.")
		log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClass] create StorageClass %+v", newSC))
		if err = CreateStorageClass(ctx, cl, newSC); err != nil {
			return true, fmt.Errorf("error CreateStorageClass %s: %w", replicatedSC.Name, err)
		}
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " created.")
	} else {
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: Update " + replicatedSC.Name +
			" storage class if needed.")
		shouldRequeue, err := UpdateStorageClassIfNeeded(ctx, cl, log, newSC, oldSC)
		if err != nil {
			return shouldRequeue, fmt.Errorf("error updateStorageClassIfNeeded: %w", err)
		}
	}

	replicatedSC.Status.Phase = Created
	replicatedSC.Status.Reason = "ReplicatedStorageClass and StorageClass are equal."
	if !slices.Contains(replicatedSC.ObjectMeta.Finalizers, ReplicatedStorageClassFinalizerName) {
		replicatedSC.ObjectMeta.Finalizers = append(replicatedSC.ObjectMeta.Finalizers,
			ReplicatedStorageClassFinalizerName)
	}
	log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClassEvent] update ReplicatedStorageClass %+v", replicatedSC))
	if err = UpdateReplicatedStorageClass(ctx, cl, replicatedSC); err != nil {
		err = fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error UpdateReplicatedStorageClass: %w", err)
		return true, err
	}

	return false, nil
}

func ReconcileDeleteReplicatedStorageClass(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	replicatedSC *srv.ReplicatedStorageClass,
	sc *storagev1.StorageClass,
) (bool, error) {
	switch replicatedSC.Status.Phase {
	case Failed:
		log.Info("[ReconcileDeleteReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name +
			" was not deleted because the ReplicatedStorageClass is in a Failed state. Deleting only finalizer.")
	case Created:
		if sc == nil {
			log.Info("[ReconcileDeleteReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name +
				" no need to delete.")
			break
		}
		log.Info("[ReconcileDeleteReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name +
			" found. Deleting it.")

		if err := DeleteStorageClass(ctx, cl, sc); err != nil {
			return true, fmt.Errorf("error DeleteStorageClass: %w", err)
		}
		log.Info("[ReconcileDeleteReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name +
			" deleted.")
	}

	log.Info("[ReconcileDeleteReplicatedStorageClass] Removing finalizer from ReplicatedStorageClass with name: " +
		replicatedSC.Name)

	replicatedSC.ObjectMeta.Finalizers = RemoveString(replicatedSC.ObjectMeta.Finalizers,
		ReplicatedStorageClassFinalizerName)
	if err := UpdateReplicatedStorageClass(ctx, cl, replicatedSC); err != nil {
		return true, fmt.Errorf("error UpdateReplicatedStorageClass after removing finalizer: %w", err)
	}

	log.Info("[ReconcileDeleteReplicatedStorageClass] Finalizer removed from ReplicatedStorageClass with name: " +
		replicatedSC.Name)
	return false, nil
}

func GetClusterZones(ctx context.Context, cl client.Client) (map[string]struct{}, error) {
	nodes := v1.NodeList{}
	if err := cl.List(ctx, &nodes); err != nil {
		return nil, err
	}

	nodeZones := make(map[string]struct{}, len(nodes.Items))

	for _, node := range nodes.Items {
		if zone, exist := node.Labels[ZoneLabel]; exist {
			nodeZones[zone] = struct{}{}
		}
	}

	return nodeZones, nil
}

func ValidateReplicatedStorageClass(replicatedSC *srv.ReplicatedStorageClass, zones map[string]struct{}) (bool, string) {
	var (
		failedMsgBuilder strings.Builder
		validationPassed = true
	)

	failedMsgBuilder.WriteString("Validation of ReplicatedStorageClass failed: ")

	if replicatedSC.Spec.StoragePool == "" {
		validationPassed = false
		failedMsgBuilder.WriteString("StoragePool is empty; ")
	}

	if replicatedSC.Spec.ReclaimPolicy == "" {
		validationPassed = false
		failedMsgBuilder.WriteString("ReclaimPolicy is empty; ")
	}

	switch replicatedSC.Spec.Topology {
	case TopologyTransZonal:
		if len(replicatedSC.Spec.Zones) == 0 {
			validationPassed = false
			failedMsgBuilder.WriteString("Topology is set to 'TransZonal', but zones are not specified; ")
		} else {
			switch replicatedSC.Spec.Replication {
			case ReplicationAvailability, ReplicationConsistencyAndAvailability:
				if len(replicatedSC.Spec.Zones) != 3 {
					validationPassed = false
					failedMsgBuilder.WriteString(fmt.Sprintf("Selected unacceptable amount of zones for replication type: %s; correct number of zones should be 3; ", replicatedSC.Spec.Replication))
				}
			case ReplicationNone:
			default:
				validationPassed = false
				failedMsgBuilder.WriteString(fmt.Sprintf("Selected unsupported replication type: %s; ", replicatedSC.Spec.Replication))
			}
		}
	case TopologyZonal:
		if len(replicatedSC.Spec.Zones) != 0 {
			validationPassed = false
			failedMsgBuilder.WriteString("Topology is set to 'Zonal', but zones are specified; ")
		}
	case TopologyIgnored:
		if len(zones) > 0 {
			validationPassed = false
			failedMsgBuilder.WriteString("Setting 'topology' to 'Ignored' is prohibited when zones are present in the cluster; ")
		}
		if len(replicatedSC.Spec.Zones) != 0 {
			validationPassed = false
			failedMsgBuilder.WriteString("Topology is set to 'Ignored', but zones are specified; ")
		}
	default:
		validationPassed = false
		failedMsgBuilder.WriteString(fmt.Sprintf("Selected unsupported topology: %s; ", replicatedSC.Spec.Topology))
	}

	return validationPassed, failedMsgBuilder.String()
}

func UpdateReplicatedStorageClass(ctx context.Context, cl client.Client, replicatedSC *srv.ReplicatedStorageClass) error {
	err := cl.Update(ctx, replicatedSC)
	if err != nil {
		return err
	}
	return nil
}

func CompareStorageClasses(newSC, oldSC *storagev1.StorageClass) (bool, string) {
	var (
		failedMsgBuilder strings.Builder
		equal            = true
	)

	failedMsgBuilder.WriteString("Old StorageClass and New StorageClass are not equal: ")

	if !reflect.DeepEqual(oldSC.Parameters, newSC.Parameters) {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("Parameters are not equal (ReplicatedStorageClass parameters: %+v, StorageClass parameters: %+v); ", newSC.Parameters, oldSC.Parameters))
	}

	if oldSC.Provisioner != newSC.Provisioner {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("Provisioner are not equal (Old StorageClass: %s, New StorageClass: %s); ", oldSC.Provisioner, newSC.Provisioner))
	}

	if *oldSC.ReclaimPolicy != *newSC.ReclaimPolicy {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("ReclaimPolicy are not equal (Old StorageClass: %s, New StorageClass: %s", string(*oldSC.ReclaimPolicy), string(*newSC.ReclaimPolicy)))
	}

	if *oldSC.VolumeBindingMode != *newSC.VolumeBindingMode {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("VolumeBindingMode are not equal (Old StorageClass: %s, New StorageClass: %s); ", string(*oldSC.VolumeBindingMode), string(*newSC.VolumeBindingMode)))
	}

	return equal, failedMsgBuilder.String()
}

func CreateStorageClass(ctx context.Context, cl client.Client, newStorageClass *storagev1.StorageClass) error {
	err := cl.Create(ctx, newStorageClass)
	if err != nil {
		return err
	}
	return nil
}

func GetReplicatedStoragePoolData(ctx context.Context, cl client.Client, replicatedSC *srv.ReplicatedStorageClass) (map[string]string, error) {
	result := map[string]string{}
	type ThinPool struct {
		PoolName string `yaml:"poolName"`
	}
	type LVMVolumeGroup struct {
		Name string   `yaml:"name"`
		Thin ThinPool `yaml:"Thin"`
	}

	cwt, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rsp := &srv.ReplicatedStoragePool{}
	err := cl.Get(cwt, client.ObjectKey{Name: replicatedSC.Spec.StoragePool, Namespace: replicatedSC.Namespace}, rsp)
	if err != nil {
		fmt.Printf("[GenerateStorageClassFromReplicatedStorageClass] failed to get ReplicatedStoragePools %s", replicatedSC.Spec.StoragePool)
		return result, err
	}

	rscLVGs := make([]LVMVolumeGroup, 0, len(rsp.Spec.LVMVolumeGroups))
	for _, val := range rsp.Spec.LVMVolumeGroups {
		rscLVGs = append(rscLVGs, LVMVolumeGroup{
			Name: val.Name,
			Thin: ThinPool{PoolName: val.ThinPoolName},
		})
	}

	rscLVGsStr, err := json.Marshal(rscLVGs)
	if err != nil {
		fmt.Printf("[GenerateStorageClassFromReplicatedStorageClass] failed to marshal LVMVolumeGroups: %s", err.Error())
		return result, err
	}

	result["LVGs"] = string(rscLVGsStr)
	result["Type"] = rsp.Spec.Type

	return result, nil
}

func GenerateStorageClassFromReplicatedStorageClass(replicatedSC *srv.ReplicatedStorageClass) *storagev1.StorageClass {
	allowVolumeExpansion := true
	reclaimPolicy := v1.PersistentVolumeReclaimPolicy(replicatedSC.Spec.ReclaimPolicy)

	storageClassParameters := map[string]string{
		StorageClassParamFSTypeKey:                     FsTypeExt4,
		StorageClassStoragePoolKey:                     replicatedSC.Spec.StoragePool,
		StorageClassParamPlacementPolicyKey:            PlacementPolicyAutoPlaceTopology,
		StorageClassParamNetProtocolKey:                NetProtocolC,
		StorageClassParamNetRRConflictKey:              RrConflictRetryConnect,
		StorageClassParamAutoAddQuorumTieBreakerKey:    "true",
		StorageClassParamOnNoQuorumKey:                 SuspendIo,
		StorageClassParamOnNoDataAccessibleKey:         SuspendIo,
		StorageClassParamOnSuspendedPrimaryOutdatedKey: PrimaryOutdatedForceSecondary,
		ReplicatedStorageClassParamNameKey:             replicatedSC.Name,
	}

	fmt.Printf("[GenerateStorageClassFromReplicatedStorageClass] storageClassParameters %s", storageClassParameters[ReplicatedStorageClassParamNameKey])

	switch replicatedSC.Spec.Replication {
	case ReplicationNone:
		storageClassParameters[StorageClassPlacementCountKey] = "1"
		storageClassParameters[StorageClassAutoEvictMinReplicaCountKey] = "1"
		storageClassParameters[StorageClassParamAutoQuorumKey] = SuspendIo
	case ReplicationAvailability:
		storageClassParameters[StorageClassPlacementCountKey] = "2"
		storageClassParameters[StorageClassAutoEvictMinReplicaCountKey] = "2"
		storageClassParameters[StorageClassParamAutoQuorumKey] = SuspendIo
	case ReplicationConsistencyAndAvailability:
		storageClassParameters[StorageClassPlacementCountKey] = "3"
		storageClassParameters[StorageClassAutoEvictMinReplicaCountKey] = "3"
		storageClassParameters[StorageClassParamAutoQuorumKey] = SuspendIo
		storageClassParameters[QuorumMinimumRedundancyWithPrefixSCKey] = "2"
	}

	var volumeBindingMode storagev1.VolumeBindingMode
	switch replicatedSC.Spec.VolumeAccess {
	case VolumeAccessLocal:
		storageClassParameters[StorageClassParamAllowRemoteVolumeAccessKey] = "false"
		volumeBindingMode = "WaitForFirstConsumer"
	case VolumeAccessEventuallyLocal:
		storageClassParameters[StorageClassParamAutoDiskfulKey] = "30"
		storageClassParameters[StorageClassParamAutoDiskfulAllowCleanupKey] = "true"
		storageClassParameters[StorageClassParamAllowRemoteVolumeAccessKey] = StorageClassParamAllowRemoteVolumeAccessValue
		volumeBindingMode = "WaitForFirstConsumer"
	case VolumeAccessPreferablyLocal:
		storageClassParameters[StorageClassParamAllowRemoteVolumeAccessKey] = StorageClassParamAllowRemoteVolumeAccessValue
		volumeBindingMode = "WaitForFirstConsumer"
	case VolumeAccessAny:
		storageClassParameters[StorageClassParamAllowRemoteVolumeAccessKey] = StorageClassParamAllowRemoteVolumeAccessValue
		volumeBindingMode = "Immediate"
	}

	switch replicatedSC.Spec.Topology {
	case TopologyTransZonal:
		storageClassParameters[StorageClassParamReplicasOnSameKey] = fmt.Sprintf("%s/%s", StorageClassLabelKeyPrefix, replicatedSC.Name)
		storageClassParameters[StorageClassParamReplicasOnDifferentKey] = ZoneLabel
	case TopologyZonal:
		storageClassParameters[StorageClassParamReplicasOnSameKey] = ZoneLabel
		storageClassParameters[StorageClassParamReplicasOnDifferentKey] = "kubernetes.io/hostname"
	case TopologyIgnored:
		storageClassParameters[StorageClassParamReplicasOnDifferentKey] = "kubernetes.io/hostname"
	}

	newStorageClass := &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       StorageClassKind,
			APIVersion: StorageClassAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            replicatedSC.Name,
			Namespace:       replicatedSC.Namespace,
			OwnerReferences: nil,
			Finalizers:      []string{StorageClassFinalizerName},
			ManagedFields:   nil,
			Labels:          map[string]string{ManagedLabelKey: ManagedLabelValue},
			Annotations:     nil,
		},
		AllowVolumeExpansion: &allowVolumeExpansion,
		Parameters:           storageClassParameters,
		Provisioner:          StorageClassProvisioner,
		ReclaimPolicy:        &reclaimPolicy,
		VolumeBindingMode:    &volumeBindingMode,
	}

	return newStorageClass
}

func GetReplicatedStorageClass(ctx context.Context, cl client.Client, namespace, name string) (*srv.ReplicatedStorageClass, error) {
	replicatedSC := &srv.ReplicatedStorageClass{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, replicatedSC)

	if err != nil {
		return nil, err
	}

	return replicatedSC, err
}

func GetStorageClass(ctx context.Context, cl client.Client, name string) (*storagev1.StorageClass, error) {
	sc := &storagev1.StorageClass{}
	err := cl.Get(ctx, client.ObjectKey{
		Name: name,
	}, sc)

	if err != nil {
		return nil, err
	}

	return sc, nil
}

func DeleteStorageClass(ctx context.Context, cl client.Client, sc *storagev1.StorageClass) error {
	finalizers := sc.ObjectMeta.Finalizers
	switch len(finalizers) {
	case 0:
		return cl.Delete(ctx, sc)
	case 1:
		if finalizers[0] != StorageClassFinalizerName {
			return fmt.Errorf("deletion of StorageClass with finalizer %s is not allowed", finalizers[0])
		}
		sc.ObjectMeta.Finalizers = nil
		if err := cl.Update(ctx, sc); err != nil {
			return fmt.Errorf("error updating StorageClass to remove finalizer %s: %w",
				StorageClassFinalizerName, err)
		}
		return cl.Delete(ctx, sc)
	}
	// The finalizers list contains more than one element — return an error
	return fmt.Errorf("deletion of StorageClass with multiple(%v) finalizers is not allowed", finalizers)
}

// areSlicesEqualIgnoreOrder compares two slices as sets, ignoring order
func areSlicesEqualIgnoreOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	set := make(map[string]struct{}, len(a))
	for _, item := range a {
		set[item] = struct{}{}
	}

	for _, item := range b {
		if _, found := set[item]; !found {
			return false
		}
	}

	return true
}

func updateStorageClassMetaDataIfNeeded(
	ctx context.Context,
	cl client.Client,
	newSC, oldSC *storagev1.StorageClass,
) error {
	needsUpdate := !maps.Equal(oldSC.Labels, newSC.Labels) ||
		!maps.Equal(oldSC.Annotations, newSC.Annotations) ||
		!areSlicesEqualIgnoreOrder(newSC.Finalizers, oldSC.Finalizers)

	if !needsUpdate {
		return nil
	}

	oldSC.Labels = maps.Clone(newSC.Labels)
	oldSC.Annotations = maps.Clone(newSC.Annotations)
	oldSC.Finalizers = slices.Clone(newSC.Finalizers)

	return cl.Update(ctx, oldSC)
}

func canRecreateStorageClass(newSC, oldSC *storagev1.StorageClass) (bool, string) {
	newSCCopy := newSC.DeepCopy()
	oldSCCopy := oldSC.DeepCopy()

	// We can recreate StorageClass only if the following parameters are not equal.
	// If other parameters are not equal, we can't recreate StorageClass and
	// users must delete ReplicatedStorageClass resource and create it again manually.
	delete(newSCCopy.Parameters, QuorumMinimumRedundancyWithPrefixSCKey)
	delete(newSCCopy.Parameters, ReplicatedStorageClassParamNameKey)
	delete(oldSCCopy.Parameters, QuorumMinimumRedundancyWithPrefixSCKey)
	delete(oldSCCopy.Parameters, ReplicatedStorageClassParamNameKey)
	return CompareStorageClasses(newSCCopy, oldSCCopy)
}

func recreateStorageClassIfNeeded(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	newSC, oldSC *storagev1.StorageClass,
) (isRecreated, shouldRequeue bool, err error) {
	equal, msg := CompareStorageClasses(newSC, oldSC)
	log.Trace(fmt.Sprintf("[recreateStorageClassIfNeeded] msg after compare: %s", msg))
	if equal {
		log.Info("[recreateStorageClassIfNeeded] Old and new StorageClass are equal." +
			"No need to recreate StorageClass.")
		return false, false, nil
	}

	log.Info("[recreateStorageClassIfNeeded] ReplicatedStorageClass and StorageClass are not equal." +
		"Check if StorageClass can be recreated.")
	canRecreate, msg := canRecreateStorageClass(newSC, oldSC)
	if !canRecreate {
		err := fmt.Errorf("[recreateStorageClassIfNeeded] The StorageClass cannot be recreated because "+
			"its parameters are not equal: %s", msg)
		return false, false, err
	}

	log.Info("[recreateStorageClassIfNeeded] StorageClass will be recreated.")
	if err := DeleteStorageClass(ctx, cl, oldSC); err != nil {
		err = fmt.Errorf("[recreateStorageClassIfNeeded] error DeleteStorageClass: %w", err)
		return false, true, err
	}

	log.Info("[recreateStorageClassIfNeeded] StorageClass with name: " + oldSC.Name + " deleted. Recreate it.")
	if err := CreateStorageClass(ctx, cl, newSC); err != nil {
		err = fmt.Errorf("[recreateStorageClassIfNeeded] error CreateStorageClass: %w", err)
		return false, true, err
	}
	log.Info("[recreateStorageClassIfNeeded] StorageClass with name: " + newSC.Name + " recreated.")
	return true, false, nil
}

func GetNewStorageClass(replicatedSC *srv.ReplicatedStorageClass, virtualizationEnabled bool, replicatedStoragePoolData map[string]string) *storagev1.StorageClass {
	newSC := GenerateStorageClassFromReplicatedStorageClass(replicatedSC)
	if replicatedSC.Spec.VolumeAccess == VolumeAccessLocal && virtualizationEnabled {
		if newSC.Annotations == nil {
			newSC.Annotations = make(map[string]string, 1)
		}
		newSC.Annotations[StorageClassVirtualizationAnnotationKey] = StorageClassVirtualizationAnnotationValue
	}
	newSC.Parameters[StorageClassLVMVolumeGroupsParamKey] = replicatedStoragePoolData["LVGs"]
	LVMtype := replicatedStoragePoolData["Type"]
	if LVMtype == "LVM" {
		newSC.Parameters[StorageClassLVMType] = "Thick"
	}
	if LVMtype == "LVMThin" {
		newSC.Parameters[StorageClassLVMType] = "Thin"
	}

	return newSC
}

func DoUpdateStorageClass(
	newSC *storagev1.StorageClass,
	oldSC *storagev1.StorageClass,
) {
	// Copy Labels from oldSC to newSC if they do not exist in newSC
	if len(oldSC.Labels) > 0 {
		if newSC.Labels == nil {
			newSC.Labels = maps.Clone(oldSC.Labels)
		} else {
			updateMap(newSC.Labels, oldSC.Labels)
		}
	}

	copyAnnotations := maps.Clone(oldSC.Annotations)
	delete(copyAnnotations, StorageClassVirtualizationAnnotationKey)

	// Copy relevant Annotations from oldSC to newSC, excluding StorageClassVirtualizationAnnotationKey
	if len(copyAnnotations) > 0 {
		if newSC.Annotations == nil {
			newSC.Annotations = copyAnnotations
		} else {
			updateMap(newSC.Annotations, copyAnnotations)
		}
	}

	// Copy Finalizers from oldSC to newSC, avoiding duplicates
	if len(oldSC.Finalizers) > 0 {
		finalizersSet := make(map[string]struct{}, len(newSC.Finalizers))
		for _, f := range newSC.Finalizers {
			finalizersSet[f] = struct{}{}
		}
		for _, f := range oldSC.Finalizers {
			if _, exists := finalizersSet[f]; !exists {
				newSC.Finalizers = append(newSC.Finalizers, f)
				finalizersSet[f] = struct{}{}
			}
		}
	}
}

func UpdateStorageClassIfNeeded(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	newSC *storagev1.StorageClass,
	oldSC *storagev1.StorageClass,
) (bool, error) {
	DoUpdateStorageClass(newSC, oldSC)
	log.Trace(fmt.Sprintf("[UpdateStorageClassIfNeeded] old StorageClass %+v", oldSC))
	log.Trace(fmt.Sprintf("[UpdateStorageClassIfNeeded] updated StorageClass %+v", newSC))

	isRecreated, shouldRequeue, err := recreateStorageClassIfNeeded(ctx, cl, log, newSC, oldSC)
	if err != nil || isRecreated {
		return shouldRequeue, err
	}

	if err := updateStorageClassMetaDataIfNeeded(ctx, cl, newSC, oldSC); err != nil {
		return true, err
	}

	return shouldRequeue, nil
}

func RemoveString(slice []string, s string) (result []string) {
	for _, value := range slice {
		if value != s {
			result = append(result, value)
		}
	}
	return
}

func updateReplicatedStorageClassStatus(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	replicatedSC *srv.ReplicatedStorageClass,
	phase string,
	reason string,
) error {
	replicatedSC.Status.Phase = phase
	replicatedSC.Status.Reason = reason
	log.Trace(fmt.Sprintf("[updateReplicatedStorageClassStatus] update ReplicatedStorageClass %+v", replicatedSC))
	return UpdateReplicatedStorageClass(ctx, cl, replicatedSC)
}

func updateMap(dst, src map[string]string) {
	for k, v := range src {
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}

}
