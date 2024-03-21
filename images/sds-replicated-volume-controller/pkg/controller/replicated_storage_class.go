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
	"sds-replicated-volume-controller/api/v1alpha1"
	"sds-replicated-volume-controller/pkg/logger"
	"strings"
	"time"

	"k8s.io/utils/strings/slices"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
)

const (
	ReplicatedStorageClassControllerName = "replicated-storage-class-controller"
	ReplicatedStorageClassFinalizerName  = "replicatedstorageclass.storage.deckhouse.io"
	StorageClassProvisioner              = "linstor.csi.linbit.com"
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

	StorageClassPlacementCountKey                 = "linstor.csi.linbit.com/placementCount"
	StorageClassAutoEvictMinReplicaCountKey       = "property.linstor.csi.linbit.com/DrbdOptions/AutoEvictMinReplicaCount"
	StorageClassStoragePoolKey                    = "linstor.csi.linbit.com/storagePool"
	StorageClassParamReplicasOnDifferentKey       = "linstor.csi.linbit.com/replicasOnDifferent"
	StorageClassParamReplicasOnSameKey            = "linstor.csi.linbit.com/replicasOnSame"
	StorageClassParamAllowRemoteVolumeAccessKey   = "linstor.csi.linbit.com/allowRemoteVolumeAccess"
	StorageClassParamAllowRemoteVolumeAccessValue = "- fromSame:\n  - topology.kubernetes.io/zone"

	StorageClassParamFSTypeKey                     = "csi.storage.k8s.io/fstype"
	FsTypeExt4                                     = "ext4"
	StorageClassParamPlacementPolicyKey            = "linstor.csi.linbit.com/placementPolicy"
	PlacementPolicyAutoPlaceTopology               = "AutoPlaceTopology"
	StorageClassParamNetProtocolKey                = "property.linstor.csi.linbit.com/DrbdOptions/Net/protocol"
	NetProtocolC                                   = "C"
	StorageClassParamNetRRConflictKey              = "property.linstor.csi.linbit.com/DrbdOptions/Net/rr-conflict"
	RrConflictRetryConnect                         = "retry-connect"
	StorageClassParamAutoQuorumKey                 = "property.linstor.csi.linbit.com/DrbdOptions/auto-quorum"
	SuspendIo                                      = "suspend-io"
	StorageClassParamAutoAddQuorumTieBreakerKey    = "property.linstor.csi.linbit.com/DrbdOptions/auto-add-quorum-tiebreaker"
	StorageClassParamOnNoQuorumKey                 = "property.linstor.csi.linbit.com/DrbdOptions/Resource/on-no-quorum"
	StorageClassParamOnNoDataAccessibleKey         = "property.linstor.csi.linbit.com/DrbdOptions/Resource/on-no-data-accessible"
	StorageClassParamOnSuspendedPrimaryOutdatedKey = "property.linstor.csi.linbit.com/DrbdOptions/Resource/on-suspended-primary-outdated"
	PrimaryOutdatedForceSecondary                  = "force-secondary"

	StorageClassParamAutoDiskfulKey             = "property.linstor.csi.linbit.com/DrbdOptions/auto-diskful"
	StorageClassParamAutoDiskfulAllowCleanupKey = "property.linstor.csi.linbit.com/DrbdOptions/auto-diskful-allow-cleanup"

	ManagedLabelKey   = "storage.deckhouse.io/managed-by"
	ManagedLabelValue = "sds-replicated-volume"

	Created = "Created"
	Failed  = "Failed"

	DefaultStorageClassAnnotationKey = "storageclass.kubernetes.io/is-default-class"
)

func NewReplicatedStorageClass(
	mgr manager.Manager,
	interval int,
	log logger.Logger,
) (controller.Controller, error) {
	cl := mgr.GetClient()

	c, err := controller.New(ReplicatedStorageClassControllerName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {

			log.Info("START reconcile of ReplicatedStorageClass with name: " + request.Name)

			shouldRequeue, err := ReconcileReplicatedStorageClassEvent(ctx, cl, request, log)
			if shouldRequeue {
				log.Error(err, fmt.Sprintf("error in ReconcileReplicatedStorageClassEvent. Add to retry after %d seconds.", interval))
				return reconcile.Result{Requeue: true, RequeueAfter: time.Duration(interval) * time.Second}, nil
			} else {
				log.Info("END reconcile of ReplicatedStorageClass with name: " + request.Name)
			}

			return reconcile.Result{Requeue: false}, nil
		}),
	})

	if err != nil {
		return nil, err
	}

	err = c.Watch(
		source.Kind(mgr.GetCache(), &v1alpha1.ReplicatedStorageClass{}),
		handler.Funcs{
			CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
				log.Info("START from CREATE reconcile of ReplicatedStorageClass with name: " + e.Object.GetName())

				request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
				shouldRequeue, err := ReconcileReplicatedStorageClassEvent(ctx, cl, request, log)
				if shouldRequeue {
					log.Error(err, fmt.Sprintf("error in ReconcileReplicatedStorageClassEvent. Add to retry after %d seconds.", interval))
					q.AddAfter(request, time.Duration(interval)*time.Second)
				}

				log.Info("END from CREATE reconcile of ReplicatedStorageClass with name: " + request.Name)

			},
			UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {

				newReplicatedSC, ok := e.ObjectNew.(*v1alpha1.ReplicatedStorageClass)
				if !ok {
					log.Error(err, "error get ObjectNew ReplicatedStorageClass")
				}

				oldReplicatedSC, ok := e.ObjectOld.(*v1alpha1.ReplicatedStorageClass)
				if !ok {
					log.Error(err, "error get ObjectOld ReplicatedStorageClass")
				}

				if e.ObjectNew.GetDeletionTimestamp() != nil || !reflect.DeepEqual(newReplicatedSC.Spec, oldReplicatedSC.Spec) {
					log.Info("START from UPDATE reconcile of ReplicatedStorageClass with name: " + e.ObjectNew.GetName())
					request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.ObjectNew.GetNamespace(), Name: e.ObjectNew.GetName()}}
					shouldRequeue, err := ReconcileReplicatedStorageClassEvent(ctx, cl, request, log)
					if shouldRequeue {
						log.Error(err, fmt.Sprintf("error in ReconcileReplicatedStorageClassEvent. Add to retry after %d seconds.", interval))
						q.AddAfter(request, time.Duration(interval)*time.Second)
					}
					log.Info("END from UPDATE reconcile of ReplicatedStorageClass with name: " + e.ObjectNew.GetName())
				}

			},
			DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
				log.Info("START from DELETE reconcile of ReplicatedStorageClass with name: " + e.Object.GetName())

				request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
				shouldRequeue, err := ReconcileReplicatedStorageClassEvent(ctx, cl, request, log)
				if shouldRequeue {
					log.Error(err, fmt.Sprintf("error in ReconcileReplicatedStorageClassEvent. Add to retry after %d seconds.", interval))
					q.AddAfter(request, time.Duration(interval)*time.Second)
				}

				log.Info("END from DELETE reconcile of ReplicatedStorageClass with name: " + e.Object.GetName())
			},
		})
	if err != nil {
		return nil, err
	}
	return c, err
}

func ReconcileReplicatedStorageClassEvent(ctx context.Context, cl client.Client, request reconcile.Request, log logger.Logger) (bool, error) {
	replicatedSC := &v1alpha1.ReplicatedStorageClass{}
	err := cl.Get(ctx, request.NamespacedName, replicatedSC)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("[ReconcileReplicatedStorageClassEvent] ReplicatedStorageClass with name: " + request.Name + " not found. Object was probably deleted. Removing it from queue.") // #TODO: warn
			return false, nil
		}

		return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error getting ReplicatedStorageClass: %s", err.Error())
	}

	if replicatedSC.ObjectMeta.DeletionTimestamp != nil {
		log.Info("[ReconcileReplicatedStorageClassEvent] ReplicatedStorageClass with name: " + replicatedSC.Name + " is marked for deletion. Removing it.")
		// #TODO: warn
		switch replicatedSC.Status.Phase {
		case Failed:
			log.Info("[ReconcileReplicatedStorageClassEvent] StorageClass with name: " + replicatedSC.Name + " was not deleted because the ReplicatedStorageClass is in a Failed state. Deliting only finalizer.")
		case Created:
			_, err = GetStorageClass(ctx, cl, replicatedSC.Namespace, replicatedSC.Name)
			if err != nil {
				if !errors.IsNotFound(err) {
					return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error getting StorageClass: %s", err.Error())
				}

				log.Info("[ReconcileReplicatedStorageClassEvent] StorageClass with name: " + replicatedSC.Name + " not found. No need to delete it.")
				break
			}

			log.Info("[ReconcileReplicatedStorageClassEvent] StorageClass with name: " + replicatedSC.Name + " found. Deleting it.")
			if err := DeleteStorageClass(ctx, cl, replicatedSC.Namespace, replicatedSC.Name); err != nil {
				return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error DeleteStorageClass: %s", err.Error())
			}
			log.Info("[ReconcileReplicatedStorageClassEvent] StorageClass with name: " + replicatedSC.Name + " deleted.")
		}

		log.Info("[ReconcileReplicatedStorageClassEvent] Removing finalizer from ReplicatedStorageClass with name: " + replicatedSC.Name)

		replicatedSC.ObjectMeta.Finalizers = RemoveString(replicatedSC.ObjectMeta.Finalizers, ReplicatedStorageClassFinalizerName)
		if err = UpdateReplicatedStorageClass(ctx, cl, replicatedSC); err != nil {
			return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error UpdateReplicatedStorageClass: %s", err.Error())
		}

		log.Info("[ReconcileReplicatedStorageClassEvent] Finalizer removed from ReplicatedStorageClass with name: " + replicatedSC.Name)

		return false, nil
	}

	if err = ReconcileReplicatedStorageClass(ctx, cl, log, replicatedSC); err != nil {
		return true, fmt.Errorf("[ReconcileReplicatedStorageClassEvent] error ReconcileReplicatedStorageClass: %s", err.Error())
	}

	return false, nil
}

func ReconcileReplicatedStorageClass(ctx context.Context, cl client.Client, log logger.Logger, replicatedSC *v1alpha1.ReplicatedStorageClass) error { // TODO: add shouldRequeue as returned value
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
			return fmt.Errorf("[ReconcileReplicatedStorageClass] error UpdateReplicatedStorageClass: %s", err.Error())
		}

		return err
	}
	log.Info("[ReconcileReplicatedStorageClass] ReplicatedStorageClass with name: " + replicatedSC.Name + " is valid")

	log.Info("[ReconcileReplicatedStorageClass] Try to get StorageClass with name: " + replicatedSC.Name)
	storageClass, err := GetStorageClass(ctx, cl, replicatedSC.Namespace, replicatedSC.Name)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to get storage class for ReplicatedStorageClass resource, name: %s", replicatedSC.Name))
			return fmt.Errorf("[ReconcileReplicatedStorageClass] error getting StorageClass: %s", err.Error())
		}

		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " not found. Create it.")
		if err = CreateStorageClass(ctx, cl, replicatedSC); err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to create storage class for ReplicatedStorageClass resource, name: %s", replicatedSC.Name))
			return fmt.Errorf("[ReconcileReplicatedStorageClass] error CreateStorageClass: %s", err.Error())
		}
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " created.")
	} else {
		log.Info("[ReconcileReplicatedStorageClass] StorageClass with name: " + replicatedSC.Name + " found. Compare it with ReplicatedStorageClass.")
		equal, msg := CompareReplicatedStorageClassAndStorageClass(replicatedSC, storageClass)
		if !equal {
			log.Info("[ReconcileReplicatedStorageClass] ReplicatedStorageClass and StorageClass are not equal.")
			replicatedSC.Status.Phase = Failed
			replicatedSC.Status.Reason = msg

			if err := UpdateReplicatedStorageClass(ctx, cl, replicatedSC); err != nil {
				log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to update resource, name: %s", replicatedSC.Name))
				return fmt.Errorf("[ReconcileReplicatedStorageClass] error UpdateReplicatedStorageClass: %s", err.Error())
			}
			return nil
		}

		err = ReconcileStorageClassLabels(ctx, cl, storageClass)
		if err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to reconcile storage class labels, name: %s", replicatedSC.Name))
			return fmt.Errorf("error ReconcileStorageClassLabels: %s", err.Error())
		}

		log.Info("[ReconcileReplicatedStorageClass] ReplicatedStorageClass and StorageClass are equal.")
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

	if replicatedSC.Spec.IsDefault {
		err := makeStorageClassDefault(ctx, cl, replicatedSC)
		if err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClass] unable to make storage class default, name: %s", replicatedSC.Name))
			return fmt.Errorf("error makeStorageClassDefault: %s", err.Error())
		}
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

func ValidateReplicatedStorageClass(ctx context.Context, cl client.Client, replicatedSC *v1alpha1.ReplicatedStorageClass, zones map[string]struct{}) (bool, string) {
	var (
		failedMsgBuilder strings.Builder
		validationPassed = true
	)

	failedMsgBuilder.WriteString("Validation of ReplicatedStorageClass failed: ")

	if replicatedSC.Spec.IsDefault {
		replicatedSCNames, scNames, err := findAnyDefaultStorageClassEntities(ctx, cl, replicatedSC.Name)
		if err != nil {
			validationPassed = false
			failedMsgBuilder.WriteString(fmt.Sprintf("Unable to find default ReplicatedStorageClasses and Kube StorageClasses. Error: %s; ", err.Error()))
		} else {
			if len(replicatedSCNames) > 0 || len(scNames) > 0 {
				validationPassed = false
				failedMsgBuilder.WriteString(fmt.Sprintf("Conflict with other default ReplicatedStorageClasses: %s; StorageClasses: %s", strings.Join(replicatedSCNames, ","), strings.Join(scNames, ",")))
			}
		}
	}

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

func UpdateReplicatedStorageClass(ctx context.Context, cl client.Client, replicatedSC *v1alpha1.ReplicatedStorageClass) error {
	err := cl.Update(ctx, replicatedSC)
	if err != nil {
		return err
	}
	return nil
}

func CompareReplicatedStorageClassAndStorageClass(replicatedSC *v1alpha1.ReplicatedStorageClass, storageClass *storagev1.StorageClass) (bool, string) {
	var (
		failedMsgBuilder strings.Builder
		equal            = true
	)

	failedMsgBuilder.WriteString("ReplicatedStorageClass and StorageClass are not equal: ")
	newStorageClass := GenerateStorageClassFromReplicatedStorageClass(replicatedSC)

	if !reflect.DeepEqual(storageClass.Parameters, newStorageClass.Parameters) {
		// TODO: add diff
		equal = false
		failedMsgBuilder.WriteString("Parameters are not equal; ")
	}

	if storageClass.Provisioner != newStorageClass.Provisioner {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("Provisioner are not equal(ReplicatedStorageClass: %s, StorageClass: %s); ", newStorageClass.Provisioner, storageClass.Provisioner))
	}

	if *storageClass.ReclaimPolicy != *newStorageClass.ReclaimPolicy {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("ReclaimPolicy are not equal(ReplicatedStorageClass: %s, StorageClass: %s", string(*newStorageClass.ReclaimPolicy), string(*storageClass.ReclaimPolicy)))
	}

	if *storageClass.VolumeBindingMode != *newStorageClass.VolumeBindingMode {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("VolumeBindingMode are not equal(ReplicatedStorageClass: %s, StorageClass: %s); ", string(*newStorageClass.VolumeBindingMode), string(*storageClass.VolumeBindingMode)))
	}

	return equal, failedMsgBuilder.String()
}

func CreateStorageClass(ctx context.Context, cl client.Client, replicatedSC *v1alpha1.ReplicatedStorageClass) error {
	newStorageClass := GenerateStorageClassFromReplicatedStorageClass(replicatedSC)

	err := cl.Create(ctx, newStorageClass)
	if err != nil {
		return err
	}
	return nil
}

func GenerateStorageClassFromReplicatedStorageClass(replicatedSC *v1alpha1.ReplicatedStorageClass) *storagev1.StorageClass {
	var allowVolumeExpansion bool = true
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
			Labels:          map[string]string{ManagedLabelKey: ManagedLabelValue},
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
	obj := &storagev1.StorageClass{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, obj)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func DeleteStorageClass(ctx context.Context, cl client.Client, namespace, name string) error {
	csObject := &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       StorageClassKind,
			APIVersion: StorageClassAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err := cl.Delete(ctx, csObject)
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

func findAnyDefaultStorageClassEntities(ctx context.Context, cl client.Client, currentReplicatedSCName string) (defaultReplicatedSCNames []string, defaultSCNames []string, err error) {
	replicatedSCList := &v1alpha1.ReplicatedStorageClassList{}
	err = cl.List(ctx, replicatedSCList)
	if err != nil {
		return nil, nil, err
	}

	for _, replicatedSC := range replicatedSCList.Items {
		if replicatedSC.Name != currentReplicatedSCName && replicatedSC.Spec.IsDefault {
			defaultReplicatedSCNames = append(defaultReplicatedSCNames, replicatedSC.Name)
		}
	}

	scList := &storagev1.StorageClassList{}
	err = cl.List(ctx, scList)
	if err != nil {
		return nil, nil, err
	}

	for _, sc := range scList.Items {
		isDefault := sc.Annotations[DefaultStorageClassAnnotationKey]
		if sc.Name != currentReplicatedSCName && isDefault == "true" {
			defaultSCNames = append(defaultSCNames, sc.Name)
		}
	}

	return defaultReplicatedSCNames, defaultSCNames, nil
}

func makeStorageClassDefault(ctx context.Context, cl client.Client, replicatedSC *v1alpha1.ReplicatedStorageClass) error {
	storageClassList := &storagev1.StorageClassList{}
	err := cl.List(ctx, storageClassList)
	if err != nil {
		return err
	}

	for _, sc := range storageClassList.Items {
		_, isDefault := sc.Annotations[DefaultStorageClassAnnotationKey]

		if sc.Name == replicatedSC.Name && !isDefault {
			if sc.Annotations == nil {
				sc.Annotations = make(map[string]string)
			}
			sc.Annotations[DefaultStorageClassAnnotationKey] = "true"
		} else if sc.Name != replicatedSC.Name && isDefault {
			delete(sc.Annotations, DefaultStorageClassAnnotationKey)
		} else {
			continue
		}

		err := cl.Update(ctx, &sc)
		if err != nil {
			return err
		}
	}

	return nil
}

func ReconcileStorageClassLabels(ctx context.Context, cl client.Client, storageClass *storagev1.StorageClass) error {
	needUpdate := false
	if storageClass.Labels == nil {
		storageClass.Labels = make(map[string]string)
		needUpdate = true
	}

	val, exist := storageClass.Labels[ManagedLabelKey]
	if !exist || val != ManagedLabelValue {
		needUpdate = true
	}

	if needUpdate {
		storageClass.Labels[ManagedLabelKey] = ManagedLabelValue
		err := cl.Update(ctx, storageClass)
		if err != nil {
			return err
		}
	}

	return nil
}
