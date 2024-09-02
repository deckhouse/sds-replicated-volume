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
	"reflect"
	"strings"
	"time"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"sds-replicated-volume-controller/config"
	"sds-replicated-volume-controller/pkg/logger"
)

const (
	ReplicatedStorageClassControllerName = "replicated-storage-class-controller"
	ReplicatedStorageClassFinalizerName  = "replicatedstorageclass.storage.deckhouse.io"
	StorageClassProvisioner              = "replicated.csi.storage.deckhouse.io"
	StorageClassKind                     = "StorageClass"
	StorageClassAPIVersion               = "storage.k8s.io/v1"

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

			log.Trace(fmt.Sprintf("[ReplicatedStorageClassReconciler] Try to get ReplicatedStorageClass with name: %s", request.Name))
			replicatedSC := &srv.ReplicatedStorageClass{}
			err := cl.Get(ctx, request.NamespacedName, replicatedSC)
			if err != nil {
				if errors.IsNotFound(err) {
					log.Info(fmt.Sprintf("[ReplicatedStorageClassReconciler] ReplicatedStorageClass with name: %s not found. Skip it.", request.Name))
					return reconcile.Result{}, nil
				}
				log.Error(err, fmt.Sprintf("[ReplicatedStorageClassReconciler] error getting ReplicatedStorageClass: %s. Add to retry after %d seconds.", err.Error(), cfg.ScanInterval))
				return reconcile.Result{Requeue: true, RequeueAfter: time.Duration(cfg.ScanInterval) * time.Second}, nil
			}

			shouldRequeue, err := ReconcileReplicatedStorageClassEvent(ctx, cl, cfg, replicatedSC, log)
			if err != nil {
				log.Error(err, "[ReplicatedStorageClassReconciler] error in ReconcileReplicatedStorageClassEvent. Update status of ReplicatedStorageClass.")
				replicatedSC.Status.Phase = Failed
				replicatedSC.Status.Reason = err.Error()
				if err := UpdateReplicatedStorageClass(ctx, cl, replicatedSC); err != nil {
					log.Error(err, "[ReplicatedStorageClassReconciler] unable to update resource.")
					shouldRequeue = true
				}
			}

			if shouldRequeue {
				log.Error(err, fmt.Sprintf("[ReplicatedStorageClassReconciler] ReconcileReplicatedStorageClassEvent should be reconciled again. Add to retry after %d seconds.", cfg.ScanInterval))
				return reconcile.Result{Requeue: true, RequeueAfter: time.Duration(cfg.ScanInterval) * time.Second}, nil
			}

			log.Info(fmt.Sprintf("[ReplicatedStorageClassReconciler] Finish event for ReplicatedStorageClass %s in reconciler", request.Name))

			return reconcile.Result{}, nil
		}),
	})

	if err != nil {
		return nil, err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &srv.ReplicatedStorageClass{}, handler.TypedFuncs[*srv.ReplicatedStorageClass, reconcile.Request]{
		CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[*srv.ReplicatedStorageClass], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Trace(fmt.Sprintf("[ReplicatedStorageClassReconciler] Get CREATE event for ReplicatedStorageClass %s. Add it to queue.", e.Object.GetName()))
			request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
			q.Add(request)
		},
		UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[*srv.ReplicatedStorageClass], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Trace(fmt.Sprintf("[ReplicatedStorageClassReconciler] Get UPDATE event for ReplicatedStorageClass %s. Check if it was changed.", e.ObjectNew.GetName()))
			if e.ObjectNew.GetDeletionTimestamp() != nil || !reflect.DeepEqual(e.ObjectNew.Spec, e.ObjectOld.Spec) {
				log.Trace(fmt.Sprintf("[ReplicatedStorageClassReconciler] ReplicatedStorageClass %s was changed. Add it to queue.", e.ObjectNew.GetName()))
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

func ReconcileReplicatedStorageClassEvent(ctx context.Context, cl client.Client, cfg *config.Options, replicatedSC *srv.ReplicatedStorageClass, log logger.Logger) (bool, error) {
	if replicatedSC.ObjectMeta.DeletionTimestamp != nil {
		log.Info("[ReconcileReplicatedStorageClassEvent] ReplicatedStorageClass with name: " + replicatedSC.Name + " is marked for deletion. Removing it.")
		// #TODO: warn
		switch replicatedSC.Status.Phase {
		case Failed:
			log.Info("[ReconcileReplicatedStorageClassEvent] StorageClass with name: " + replicatedSC.Name + " was not deleted because the ReplicatedStorageClass is in a Failed state. Deliting only finalizer.")
		case Created:
			sc, err := GetStorageClass(ctx, cl, replicatedSC.Namespace, replicatedSC.Name)
			if err != nil {
				return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error getting StorageClass: %s", err.Error())
			}

			if sc == nil {
				log.Info("[ReconcileReplicatedStorageClassEvent] StorageClass with name: " + replicatedSC.Name + " not found. No need to delete it.")
				break
			}

			log.Info("[ReconcileReplicatedStorageClassEvent] StorageClass with name: " + replicatedSC.Name + " found. Deleting it.")
			if err := DeleteStorageClass(ctx, cl, sc); err != nil {
				return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error DeleteStorageClass: %s", err.Error())
			}
			log.Info("[ReconcileReplicatedStorageClassEvent] StorageClass with name: " + replicatedSC.Name + " deleted.")
		}

		log.Info("[ReconcileReplicatedStorageClassEvent] Removing finalizer from ReplicatedStorageClass with name: " + replicatedSC.Name)

		replicatedSC.ObjectMeta.Finalizers = RemoveString(replicatedSC.ObjectMeta.Finalizers, ReplicatedStorageClassFinalizerName)
		if err := UpdateReplicatedStorageClass(ctx, cl, replicatedSC); err != nil {
			return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error UpdateReplicatedStorageClass: %s", err.Error())
		}

		log.Info("[ReconcileReplicatedStorageClassEvent] Finalizer removed from ReplicatedStorageClass with name: " + replicatedSC.Name)

		return false, nil
	}

	if err := ReconcileReplicatedStorageClass(ctx, cl, log, cfg, replicatedSC); err != nil {
		return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error ReconcileReplicatedStorageClass: %s", err.Error())
	}

	return false, nil
}

func ReconcileReplicatedStorageClass(ctx context.Context, cl client.Client, log logger.Logger, cfg *config.Options, replicatedSC *srv.ReplicatedStorageClass) error { // TODO: add shouldRequeue as returned value
	log.Info("[ReconcileReplicatedStorageClass] Validating ReplicatedStorageClass with name: " + replicatedSC.Name)

	zones, err := GetClusterZones(ctx, cl)
	if err != nil {
		log.Error(err, "[ReconcileReplicatedStorageClass] unable to get cluster zones")
		return err
	}

	valid, msg := ValidateReplicatedStorageClass(ctx, cl, replicatedSC, zones)
	if !valid {
		err := fmt.Errorf("[ReconcileReplicatedStorageClass] Validation of ReplicatedStorageClass failed for the resource named: %s", replicatedSC.Name)
		log.Info(fmt.Sprintf("[ReconcileReplicatedStorageClass] Validation of ReplicatedStorageClass failed for the resource named: %s, for the following reason: %s", replicatedSC.Name, msg))
		replicatedSC.Status.Phase = Failed
		replicatedSC.Status.Reason = msg
		if err := UpdateReplicatedStorageClass(ctx, cl, replicatedSC); err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to update resource, name: %s", replicatedSC.Name))
			return fmt.Errorf("[ReconcileReplicatedStorageClass] error UpdateReplicatedStorageClass: %w", err)
		}

		return err
	}
	log.Info("[ReconcileReplicatedStorageClass] ReplicatedStorageClass with name: " + replicatedSC.Name + " is valid")

	log.Info("[ReconcileReplicatedStorageClass] Try to get StorageClass with name: " + replicatedSC.Name)
	oldSC, err := GetStorageClass(ctx, cl, replicatedSC.Namespace, replicatedSC.Name)
	if err != nil {
		return fmt.Errorf("[ReconcileReplicatedStorageClass] error getting StorageClass: %w", err)
	}

	log.Trace("[ReconcileReplicatedStorageClass] Check if virtualization module is enabled and if the ReplicatedStorageClass has VolumeAccess set to Local")
	virtualizationEnabled := false
	if replicatedSC.Spec.VolumeAccess == VolumeAccessLocal {
		virtualizationEnabled, err = GetVirtualizationModuleEnabled(ctx, cl, log, types.NamespacedName{Name: controllerConfigMapName, Namespace: cfg.ControllerNamespace})
		if err != nil {
			log.Error(err, "[ReconcileReplicatedStorageClass] unable to get virtualization module enabled")
			return err
		}
		log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClass] ReplicatedStorageClass has VolumeAccess set to Local and virtualization module is %t", virtualizationEnabled))
	}

	if oldSC == nil {
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " not found. Create it.")
		newSC := getNewStorageClass(replicatedSC, virtualizationEnabled)
		if err = CreateStorageClass(ctx, cl, newSC); err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to create storage class for ReplicatedStorageClass resource, name: %s", replicatedSC.Name))
			return fmt.Errorf("[ReconcileReplicatedStorageClass] error CreateStorageClass: %w", err)
		}
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " created.")
	} else {
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " found. Recreate it if needed.")
		newSC := getUpdatedStorageClass(replicatedSC, oldSC, virtualizationEnabled)
		isRecreated, err := recreateStorageClassIfNeeded(ctx, cl, log, oldSC, newSC)
		if err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to recreate storage class for ReplicatedStorageClass resource, name: %s", replicatedSC.Name))
			return fmt.Errorf("[ReconcileReplicatedStorageClass] error recreateStorageClassIfNeeded: %w", err)
		}

		if !isRecreated {
			log.Info(fmt.Sprintf("[ReconcileReplicatedStorageClass] reconcile StorageClass %s labels and annotations if needed", newSC.Name))
			err = ReconcileStorageClassLabelsAndAnnotationsIfNeeded(ctx, cl, oldSC, newSC)
			if err != nil {
				log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to reconcile StorageClass labels and annotations for ReplicatedStorageClass resource, name: %s", replicatedSC.Name))
				return fmt.Errorf("[ReconcileReplicatedStorageClass] error ReconcileStorageClassLabels: %w", err)
			}
		}
	}

	replicatedSC.Status.Phase = Created
	replicatedSC.Status.Reason = "ReplicatedStorageClass and StorageClass are equal."
	if !slices.Contains(replicatedSC.ObjectMeta.Finalizers, ReplicatedStorageClassFinalizerName) {
		replicatedSC.ObjectMeta.Finalizers = append(replicatedSC.ObjectMeta.Finalizers, ReplicatedStorageClassFinalizerName)
	}
	err = UpdateReplicatedStorageClass(ctx, cl, replicatedSC)
	if err != nil {
		log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to update resource, name: %s", replicatedSC.Name))
		return fmt.Errorf("error UpdateReplicatedStorageClass: %s", err.Error())
	}

	return nil
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

func ValidateReplicatedStorageClass(ctx context.Context, cl client.Client, replicatedSC *srv.ReplicatedStorageClass, zones map[string]struct{}) (bool, string) {
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

func CompareStorageClasses(oldSC, newSC *storagev1.StorageClass) (bool, string) {
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
	}

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
			Finalizers:      nil,
			ManagedFields:   nil,
		},
		AllowVolumeExpansion: &allowVolumeExpansion,
		Parameters:           storageClassParameters,
		Provisioner:          StorageClassProvisioner,
		ReclaimPolicy:        &reclaimPolicy,
		VolumeBindingMode:    &volumeBindingMode,
	}

	return newStorageClass
}

func GetStorageClass(ctx context.Context, cl client.Client, namespace, name string) (*storagev1.StorageClass, error) {
	sc := &storagev1.StorageClass{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, sc)
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil, err
		}
		return nil, nil
	}
	return sc, nil
}

func DeleteStorageClass(ctx context.Context, cl client.Client, sc *storagev1.StorageClass) error {
	err := cl.Delete(ctx, sc)
	if err != nil {
		return err
	}
	return nil
}

func RemoveString(slice []string, s string) (result []string) {
	for _, value := range slice {
		if value != s {
			result = append(result, value)
		}
	}
	return
}

func ReconcileStorageClassLabelsAndAnnotationsIfNeeded(ctx context.Context, cl client.Client, oldSC, newSC *storagev1.StorageClass) error {
	if !reflect.DeepEqual(oldSC.Labels, newSC.Labels) || !reflect.DeepEqual(oldSC.Annotations, newSC.Annotations) {
		err := cl.Update(ctx, newSC)
		if err != nil {
			return err
		}
	}
	return nil
}

func canRecreateStorageClass(oldSC, newSC *storagev1.StorageClass) (bool, string) {
	// We can recreate StorageClass only if the following parameters are not equal. If other parameters are not equal, we can't recreate StorageClass and users must delete ReplicatedStorageClass resource and create it again manually.
	delete(newSC.Parameters, QuorumMinimumRedundancyWithPrefixSCKey)
	delete(oldSC.Parameters, QuorumMinimumRedundancyWithPrefixSCKey)
	return CompareStorageClasses(newSC, oldSC)
}

func recreateStorageClassIfNeeded(ctx context.Context, cl client.Client, log logger.Logger, oldSC, newSC *storagev1.StorageClass) (bool, error) {
	equal, msg := CompareStorageClasses(newSC, oldSC)
	log.Trace(fmt.Sprintf("[recreateStorageClassIfNeeded] msg after compare: %s", msg))
	if equal {
		log.Info("[recreateStorageClassIfNeeded] Old and new StorageClass are equal. No need to recreate StorageClass.")
		return false, nil
	}

	log.Info("[recreateStorageClassIfNeeded] ReplicatedStorageClass and StorageClass are not equal. Check if StorageClass can be recreated.")
	canRecreate, msg := canRecreateStorageClass(oldSC, newSC)
	if !canRecreate {
		log.Info("[recreateStorageClassIfNeeded] StorageClass can't be recreated.")
		return false, fmt.Errorf("[recreateStorageClassIfNeeded] The StorageClass cannot be recreated because its immutable parameters are not equal: %s", msg)
	}

	log.Info("[recreateStorageClassIfNeeded] StorageClass can be recreated.")
	if err := DeleteStorageClass(ctx, cl, oldSC); err != nil {
		log.Error(err, fmt.Sprintf("[recreateStorageClassIfNeeded] unable to delete storage class, name: %s", oldSC.Name))
		return false, fmt.Errorf("[recreateStorageClassIfNeeded] error DeleteStorageClass: %s", err.Error())
	}

	log.Info("[recreateStorageClassIfNeeded] StorageClass with name: " + oldSC.Name + " deleted. Recreate it.")
	if err := CreateStorageClass(ctx, cl, newSC); err != nil {
		log.Error(err, fmt.Sprintf("[recreateStorageClassIfNeeded] unable to create storage class for ReplicatedStorageClass resource, name: %s", newSC.Name))
		return false, fmt.Errorf("[recreateStorageClassIfNeeded] error CreateStorageClass: %s", err.Error())
	}
	log.Info("[recreateStorageClassIfNeeded] StorageClass with name: " + newSC.Name + " recreated.")
	return true, nil
}

func getNewStorageClass(replicatedSC *srv.ReplicatedStorageClass, virtualizationEnabled bool) *storagev1.StorageClass {
	newSC := GenerateStorageClassFromReplicatedStorageClass(replicatedSC)
	newSC.Labels = map[string]string{ManagedLabelKey: ManagedLabelValue}
	if replicatedSC.Spec.VolumeAccess == VolumeAccessLocal && virtualizationEnabled {
		newSC.Annotations = map[string]string{StorageClassAnnotationToReconcileKey: StorageClassAnnotationToReconcileValue}
	}
	return newSC

}

func getUpdatedStorageClass(replicatedSC *srv.ReplicatedStorageClass, oldSC *storagev1.StorageClass, virtualizationEnabled bool) *storagev1.StorageClass {
	newSC := GenerateStorageClassFromReplicatedStorageClass(replicatedSC)
	newSC.Labels = oldSC.Labels
	newSC.Labels[ManagedLabelKey] = ManagedLabelValue

	newSC.Annotations = oldSC.Annotations
	if replicatedSC.Spec.VolumeAccess == VolumeAccessLocal && virtualizationEnabled {
		newSC.Annotations[StorageClassAnnotationToReconcileKey] = StorageClassAnnotationToReconcileValue
	}

	return newSC
}
