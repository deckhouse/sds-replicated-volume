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
	"reflect"
	"slices"
	"strings"
	"time"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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
			shouldRequeue, err := ReconcileReplicatedStorageClassEvent(ctx, cl, log, cfg, request)
			if err != nil {
				log.Error(err, "[ReplicatedStorageClassReconciler] error in ReconcileReplicatedStorageClassEvent")
			}
			if shouldRequeue {
				log.Warning(fmt.Sprintf("[ReplicatedStorageClassReconciler] ReconcileReplicatedStorageClassEvent should be reconciled again. Add to retry after %d seconds.", cfg.ScanInterval))
				return reconcile.Result{Requeue: true, RequeueAfter: time.Duration(cfg.ScanInterval) * time.Second}, nil
			}

			log.Info(fmt.Sprintf("[ReplicatedStorageClassReconciler] Finish event for ReplicatedStorageClass %s in reconciler. No need to reconcile it again.", request.Name))
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

func ReconcileReplicatedStorageClassEvent(ctx context.Context, cl client.Client, log logger.Logger, cfg *config.Options, request reconcile.Request) (bool, error) {
	log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClassEvent] Try to get ReplicatedStorageClass with name: %s", request.Name))

	replicatedSC, err := GetReplicatedStorageClass(ctx, cl, request.Namespace, request.Name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info(fmt.Sprintf("[ReconcileReplicatedStorageClassEvent] ReplicatedStorageClass with name: %s not found. Finish reconcile.", request.Name))
			return false, nil
		}

		return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error getting ReplicatedStorageClass: %w", err)
	}

	shouldRequeue, err := ReconcileReplicatedStorageClass(ctx, cl, log, cfg, replicatedSC)
	if err != nil {
		replicatedSC.Status.Phase = Failed
		replicatedSC.Status.Reason = err.Error()
		log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClass] update ReplicatedStorageClass %+v", replicatedSC))
		if updateErr := UpdateReplicatedStorageClass(ctx, cl, replicatedSC); updateErr != nil {
			// save err and add new error to it
			err = errors.Join(err, updateErr)
			err = fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error after ReconcileReplicatedStorageClass and error after UpdateReplicatedStorageClass: %w", err)
			shouldRequeue = true
		}
	}

	return shouldRequeue, err
}

func ReconcileReplicatedStorageClass(ctx context.Context, cl client.Client, log logger.Logger, cfg *config.Options, replicatedSC *srv.ReplicatedStorageClass) (bool, error) {
	if replicatedSC.ObjectMeta.DeletionTimestamp != nil {
		log.Info("[ReconcileReplicatedStorageClass] ReplicatedStorageClass with name: " + replicatedSC.Name + " is marked for deletion. Removing it.")
		switch replicatedSC.Status.Phase {
		case Failed:
			log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " was not deleted because the ReplicatedStorageClass is in a Failed state. Deleting only finalizer.")
		case Created:
			sc, err := GetStorageClass(ctx, cl, replicatedSC.Namespace, replicatedSC.Name)
			if err != nil {
				if k8serrors.IsNotFound(err) {
					log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " not found. No need to delete it.")
					break
				}
				return true, fmt.Errorf("[ReconcileReplicatedStorageClass] error getting StorageClass: %s", err.Error())
			}

			log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " found. Deleting it.")
			if err := DeleteStorageClass(ctx, cl, sc); err != nil {
				return true, fmt.Errorf("[ReconcileReplicatedStorageClass] error DeleteStorageClass: %s", err.Error())
			}
			log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " deleted.")
		}

		log.Info("[ReconcileReplicatedStorageClass] Removing finalizer from ReplicatedStorageClass with name: " + replicatedSC.Name)

		replicatedSC.ObjectMeta.Finalizers = RemoveString(replicatedSC.ObjectMeta.Finalizers, ReplicatedStorageClassFinalizerName)
		if err := UpdateReplicatedStorageClass(ctx, cl, replicatedSC); err != nil {
			return true, fmt.Errorf("[ReconcileReplicatedStorageClass] error UpdateReplicatedStorageClass after removing finalizer: %s", err.Error())
		}

		log.Info("[ReconcileReplicatedStorageClass] Finalizer removed from ReplicatedStorageClass with name: " + replicatedSC.Name)
		return false, nil
	}

	log.Info("[ReconcileReplicatedStorageClass] Validating ReplicatedStorageClass with name: " + replicatedSC.Name)

	zones, err := GetClusterZones(ctx, cl)
	if err != nil {
		err = fmt.Errorf("[ReconcileReplicatedStorageClass] error GetClusterZones: %w", err)
		return true, err
	}

	valid, msg := ValidateReplicatedStorageClass(replicatedSC, zones)
	if !valid {
		err := fmt.Errorf("[ReconcileReplicatedStorageClass] Validation of ReplicatedStorageClass %s failed for the following reason: %s", replicatedSC.Name, msg)
		return false, err
	}
	log.Info("[ReconcileReplicatedStorageClass] ReplicatedStorageClass with name: " + replicatedSC.Name + " is valid")

	log.Info("[ReconcileReplicatedStorageClass] Try to get StorageClass with name: " + replicatedSC.Name)
	oldSC, err := GetStorageClass(ctx, cl, replicatedSC.Namespace, replicatedSC.Name)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return true, fmt.Errorf("[ReconcileReplicatedStorageClass] error getting StorageClass: %w", err)
		}
	}

	log.Trace("[ReconcileReplicatedStorageClass] Check if virtualization module is enabled and if the ReplicatedStorageClass has VolumeAccess set to Local")
	var virtualizationEnabled bool
	if replicatedSC.Spec.VolumeAccess == VolumeAccessLocal {
		virtualizationEnabled, err = GetVirtualizationModuleEnabled(ctx, cl, log, types.NamespacedName{Name: ControllerConfigMapName, Namespace: cfg.ControllerNamespace})
		if err != nil {
			err = fmt.Errorf("[ReconcileReplicatedStorageClass] error GetVirtualizationModuleEnabled: %w", err)
			return true, err
		}
		log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClass] ReplicatedStorageClass has VolumeAccess set to Local and virtualization module is %t", virtualizationEnabled))
	}

	if oldSC == nil {
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " not found. Create it.")
		newSC := GetNewStorageClass(replicatedSC, virtualizationEnabled)
		log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClass] create StorageClass %+v", newSC))
		if err = CreateStorageClass(ctx, cl, newSC); err != nil {
			return true, fmt.Errorf("[ReconcileReplicatedStorageClass] error CreateStorageClass %s: %w", replicatedSC.Name, err)
		}
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " created.")
	} else {
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " found. Update it if needed.")

		shouldRequeue, err := UpdateStorageClassIfNeeded(ctx, cl, log, replicatedSC, oldSC, virtualizationEnabled)
		if err != nil {
			return shouldRequeue, fmt.Errorf("[ReconcileReplicatedStorageClass] error updateStorageClassIfNeeded: %w", err)
		}
	}

	replicatedSC.Status.Phase = Created
	replicatedSC.Status.Reason = "ReplicatedStorageClass and StorageClass are equal."
	if !slices.Contains(replicatedSC.ObjectMeta.Finalizers, ReplicatedStorageClassFinalizerName) {
		replicatedSC.ObjectMeta.Finalizers = append(replicatedSC.ObjectMeta.Finalizers, ReplicatedStorageClassFinalizerName)
	}
	log.Trace(fmt.Sprintf("[ReconcileReplicatedStorageClassEvent] update ReplicatedStorageClass %+v", replicatedSC))
	if err = UpdateReplicatedStorageClass(ctx, cl, replicatedSC); err != nil {
		err = fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error UpdateReplicatedStorageClass: %w", err)
		return true, err
	}

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

func GetReplicatedStorageClass(ctx context.Context, cl client.Client, namespace, name string) (*srv.ReplicatedStorageClass, error) {
	replicatedSC := &srv.ReplicatedStorageClass{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, replicatedSC)

	return replicatedSC, err
}

func GetStorageClass(ctx context.Context, cl client.Client, namespace, name string) (*storagev1.StorageClass, error) {
	sc := &storagev1.StorageClass{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, sc)

	if err != nil {
		return nil, err
	}

	return sc, nil
}

func DeleteStorageClass(ctx context.Context, cl client.Client, sc *storagev1.StorageClass) error {
	return cl.Delete(ctx, sc)
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
		oldSC.Labels = newSC.Labels
		oldSC.Annotations = newSC.Annotations
		return cl.Update(ctx, oldSC)
	}
	return nil
}

func canRecreateStorageClass(oldSC, newSC *storagev1.StorageClass) (bool, string) {
	newSCCopy := newSC.DeepCopy()
	oldSCCopy := oldSC.DeepCopy()

	// We can recreate StorageClass only if the following parameters are not equal. If other parameters are not equal, we can't recreate StorageClass and users must delete ReplicatedStorageClass resource and create it again manually.
	delete(newSCCopy.Parameters, QuorumMinimumRedundancyWithPrefixSCKey)
	delete(oldSCCopy.Parameters, QuorumMinimumRedundancyWithPrefixSCKey)
	return CompareStorageClasses(newSCCopy, oldSCCopy)
}

func recreateStorageClassIfNeeded(ctx context.Context, cl client.Client, log logger.Logger, oldSC, newSC *storagev1.StorageClass) (isRecreated, shouldRequeue bool, err error) {
	equal, msg := CompareStorageClasses(newSC, oldSC)
	log.Trace(fmt.Sprintf("[recreateStorageClassIfNeeded] msg after compare: %s", msg))
	if equal {
		log.Info("[recreateStorageClassIfNeeded] Old and new StorageClass are equal. No need to recreate StorageClass.")
		return false, false, nil
	}

	log.Info("[recreateStorageClassIfNeeded] ReplicatedStorageClass and StorageClass are not equal. Check if StorageClass can be recreated.")
	canRecreate, msg := canRecreateStorageClass(oldSC, newSC)
	if !canRecreate {
		err := fmt.Errorf("[recreateStorageClassIfNeeded] The StorageClass cannot be recreated because its parameters are not equal: %s", msg)
		return false, false, err
	}

	log.Info("[recreateStorageClassIfNeeded] StorageClass can be recreated.")
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

func GetNewStorageClass(replicatedSC *srv.ReplicatedStorageClass, virtualizationEnabled bool) *storagev1.StorageClass {
	newSC := GenerateStorageClassFromReplicatedStorageClass(replicatedSC)
	newSC.Labels = map[string]string{ManagedLabelKey: ManagedLabelValue}
	if replicatedSC.Spec.VolumeAccess == VolumeAccessLocal && virtualizationEnabled {
		newSC.Annotations = map[string]string{StorageClassVirtualizationAnnotationKey: StorageClassVirtualizationAnnotationValue}
	}
	return newSC
}

func GetUpdatedStorageClass(replicatedSC *srv.ReplicatedStorageClass, oldSC *storagev1.StorageClass, virtualizationEnabled bool) *storagev1.StorageClass {
	newSC := GenerateStorageClassFromReplicatedStorageClass(replicatedSC)

	newSC.Labels = make(map[string]string, len(oldSC.Labels))
	for k, v := range oldSC.Labels {
		newSC.Labels[k] = v
	}
	newSC.Labels[ManagedLabelKey] = ManagedLabelValue

	newSC.Annotations = make(map[string]string, len(oldSC.Annotations))
	for k, v := range oldSC.Annotations {
		newSC.Annotations[k] = v
	}

	if replicatedSC.Spec.VolumeAccess == VolumeAccessLocal && virtualizationEnabled {
		newSC.Annotations[StorageClassVirtualizationAnnotationKey] = StorageClassVirtualizationAnnotationValue
	} else {
		delete(newSC.Annotations, StorageClassVirtualizationAnnotationKey)
	}

	if len(newSC.Annotations) == 0 {
		newSC.Annotations = nil
	}

	return newSC
}

func UpdateStorageClassIfNeeded(ctx context.Context, cl client.Client, log logger.Logger, replicatedSC *srv.ReplicatedStorageClass, oldSC *storagev1.StorageClass, virtualizationEnabled bool) (bool, error) {
	newSC := GetUpdatedStorageClass(replicatedSC, oldSC, virtualizationEnabled)
	log.Trace(fmt.Sprintf("[UpdateStorageClassIfNeeded] old StorageClass %+v", oldSC))
	log.Trace(fmt.Sprintf("[UpdateStorageClassIfNeeded] updated StorageClass %+v", newSC))

	isRecreated, shouldRequeue, err := recreateStorageClassIfNeeded(ctx, cl, log, oldSC, newSC)
	if err != nil {
		return shouldRequeue, err
	}

	if !isRecreated {
		err := ReconcileStorageClassLabelsAndAnnotationsIfNeeded(ctx, cl, oldSC, newSC)
		if err != nil {
			return true, err
		}
	}

	return shouldRequeue, nil
}
