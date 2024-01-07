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
	"sds-drbd-controller/api/v1alpha1"
	"strings"
	"time"

	"k8s.io/utils/strings/slices"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	DRBDStorageClassControllerName = "drbd-storage-class-controller"
	DRBDStorageClassFinalizerName  = "drbdstorageclass.storage.deckhouse.io"
	StorageClassProvisioner        = "linstor.csi.linbit.com"
	StorageClassKind               = "StorageClass"
	StorageClassAPIVersion         = "storage.k8s.io/v1"

	ZoneLabel         = "topology.kubernetes.io/zone"
	ClassStorageLabel = "class.storage.deckhouse.io"

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

	// DRBDControllerPlacementCount = "linstor.csi.linbit.com/placementCount"
	// DRBDOperatorStoragePool        = "linstor.csi.linbit.com/storagePool"
	// AutoQuorum                     = "property.linstor.csi.linbit.com/DrbdOptions/auto-quorum"

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

	Created = "Created"
	Failed  = "Failed"
)

func NewDRBDStorageClass(
	mgr manager.Manager,
	interval int,
) (controller.Controller, error) {
	cl := mgr.GetClient()
	log := mgr.GetLogger()

	c, err := controller.New(DRBDStorageClassControllerName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {

			log.Info("START reconcile of DRBDStorageClass with name: " + request.Name)

			shouldRequeue, err := ReconcileDRBDStorageClassEvent(ctx, cl, request, log)
			if shouldRequeue {
				log.Error(err, fmt.Sprintf("error in ReconcileDRBDStorageClassEvent. Add to retry after %d seconds.", interval))
				return reconcile.Result{Requeue: true, RequeueAfter: time.Duration(interval) * time.Second}, nil
			} else {
				log.Info("END reconcile of DRBDStorageClass with name: " + request.Name)
			}

			return reconcile.Result{Requeue: false}, nil
		}),
	})

	if err != nil {
		return nil, err
	}

	err = c.Watch(
		source.Kind(mgr.GetCache(), &v1alpha1.DRBDStorageClass{}),
		handler.Funcs{
			CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
				log.Info("START from CREATE reconcile of DRBDStorageClass with name: " + e.Object.GetName())

				request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
				shouldRequeue, err := ReconcileDRBDStorageClassEvent(ctx, cl, request, log)
				if shouldRequeue {
					log.Error(err, fmt.Sprintf("error in ReconcileDRBDStorageClassEvent. Add to retry after %d seconds.", interval))
					q.AddAfter(request, time.Duration(interval)*time.Second)
				}

				log.Info("END from CREATE reconcile of DRBDStorageClass with name: " + request.Name)

			},
			UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {

				newDRBDSC, ok := e.ObjectNew.(*v1alpha1.DRBDStorageClass)
				if !ok {
					log.Error(err, "error get ObjectNew DRBDStorageClass")
				}

				oldDRBDSC, ok := e.ObjectOld.(*v1alpha1.DRBDStorageClass)
				if !ok {
					log.Error(err, "error get ObjectOld DRBDStorageClass")
				}

				if e.ObjectNew.GetDeletionTimestamp() != nil || !reflect.DeepEqual(newDRBDSC.Spec, oldDRBDSC.Spec) {
					log.Info("START from UPDATE reconcile of DRBDStorageClass with name: " + e.ObjectNew.GetName())
					request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.ObjectNew.GetNamespace(), Name: e.ObjectNew.GetName()}}
					shouldRequeue, err := ReconcileDRBDStorageClassEvent(ctx, cl, request, log)
					if shouldRequeue {
						log.Error(err, fmt.Sprintf("error in ReconcileDRBDStorageClassEvent. Add to retry after %d seconds.", interval))
						q.AddAfter(request, time.Duration(interval)*time.Second)
					}
					log.Info("END from UPDATE reconcile of DRBDStorageClass with name: " + e.ObjectNew.GetName())
				}

			},
			DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
				log.Info("START from DELETE reconcile of DRBDStorageClass with name: " + e.Object.GetName())

				request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
				shouldRequeue, err := ReconcileDRBDStorageClassEvent(ctx, cl, request, log)
				if shouldRequeue {
					log.Error(err, fmt.Sprintf("error in ReconcileDRBDStorageClassEvent. Add to retry after %d seconds.", interval))
					q.AddAfter(request, time.Duration(interval)*time.Second)
				}

				log.Info("END from DELETE reconcile of DRBDStorageClass with name: " + e.Object.GetName())
			},
		})
	if err != nil {
		return nil, err
	}
	return c, err
}

func ReconcileDRBDStorageClassEvent(ctx context.Context, cl client.Client, request reconcile.Request, log logr.Logger) (bool, error) {
	drbdsc := &v1alpha1.DRBDStorageClass{}
	err := cl.Get(ctx, request.NamespacedName, drbdsc)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("[ReconcileDRBDStorageClassEvent] DRBDStorageClass with name: " + request.Name + " not found. Object was probably deleted. Removing it from queue.") // #TODO: warn
			return false, nil
		}

		return true, fmt.Errorf("[ReconcileDRBDStorageClassEvent] error getting DRBDStorageClass: %s", err.Error())
	}

	if drbdsc.ObjectMeta.DeletionTimestamp != nil {
		log.Info("[ReconcileDRBDStorageClassEvent] DRBDStorageClass with name: " + drbdsc.Name + " is marked for deletion. Removing it.")
		// #TODO: warn
		switch drbdsc.Status.Phase {
		case Failed:
			log.Info("[ReconcileDRBDStorageClassEvent] StorageClass with name: " + drbdsc.Name + " was not deleted because the DRBDStorageClass is in a Failed state. Deliting only finalizer.")
		case Created:
			_, err = GetStorageClass(ctx, cl, drbdsc.Namespace, drbdsc.Name)
			if err != nil {
				if !errors.IsNotFound(err) {
					return true, fmt.Errorf("[ReconcileDRBDStorageClassEvent] error getting StorageClass: %s", err.Error())
				}

				log.Info("[ReconcileDRBDStorageClassEvent] StorageClass with name: " + drbdsc.Name + " not found. No need to delete it.")
				break
			}

			log.Info("[ReconcileDRBDStorageClassEvent] StorageClass with name: " + drbdsc.Name + " found. Deleting it.")
			if err := DeleteStorageClass(ctx, cl, drbdsc.Namespace, drbdsc.Name); err != nil {
				return true, fmt.Errorf("[ReconcileDRBDStorageClassEvent] error DeleteStorageClass: %s", err.Error())
			}
			log.Info("[ReconcileDRBDStorageClassEvent] StorageClass with name: " + drbdsc.Name + " deleted.")
		}

		log.Info("[ReconcileDRBDStorageClassEvent] Removing finalizer from DRBDStorageClass with name: " + drbdsc.Name)

		drbdsc.ObjectMeta.Finalizers = RemoveString(drbdsc.ObjectMeta.Finalizers, DRBDStorageClassFinalizerName)
		if err = UpdateDRBDStorageClass(ctx, cl, drbdsc); err != nil {
			return true, fmt.Errorf("[ReconcileDRBDStorageClassEvent] error UpdateDRBDStorageClass: %s", err.Error())
		}

		log.Info("[ReconcileDRBDStorageClassEvent] Finalizer removed from DRBDStorageClass with name: " + drbdsc.Name)

		return false, nil
	}

	if err = ReconcileDRBDStorageClass(ctx, cl, log, drbdsc); err != nil {
		return true, fmt.Errorf("[ReconcileDRBDStorageClassEvent] error ReconcileDRBDStorageClass: %s", err.Error())
	}

	return false, nil
}

func ReconcileDRBDStorageClass(ctx context.Context, cl client.Client, log logr.Logger, drbdsc *v1alpha1.DRBDStorageClass) error { // TODO: add shouldRequeue as returned value
	log.Info("[ReconcileDRBDStorageClass] Validating DRBDStorageClass with name: " + drbdsc.Name)

	zones, err := GetClusterZones(ctx, cl)
	if err != nil {
		log.Error(err, "[ReconcileDRBDStorageClass] unable to get cluster zones")
		return err
	}

	valid, msg := ValidateDRBDStorageClass(ctx, cl, drbdsc, zones)
	if !valid {
		err := fmt.Errorf("[ReconcileDRBDStorageClass] Validation of DRBDStorageClass failed for the resource named: %s", drbdsc.Name)
		log.Info(fmt.Sprintf("[ReconcileDRBDStorageClass] Validation of DRBDStorageClass failed for the resource named: %s, for the following reason: %s", drbdsc.Name, msg))
		drbdsc.Status.Phase = Failed
		drbdsc.Status.Reason = msg
		if err := UpdateDRBDStorageClass(ctx, cl, drbdsc); err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClass] unable to update DRBD resource, name: %s", drbdsc.Name))
			return fmt.Errorf("[ReconcileDRBDStorageClass] error UpdateDRBDStorageClass: %s", err.Error())
		}

		return err
	}
	log.Info("[ReconcileDRBDStorageClass] DRBDStorageClass with name: " + drbdsc.Name + " is valid")

	log.Info("[ReconcileDRBDStorageClass] Try to get StorageClass with name: " + drbdsc.Name)
	storageClass, err := GetStorageClass(ctx, cl, drbdsc.Namespace, drbdsc.Name)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClass] unable to get storage class for DRBDStorageClass resource, name: %s", drbdsc.Name))
			return fmt.Errorf("[ReconcileDRBDStorageClass] error getting StorageClass: %s", err.Error())
		}

		log.Info("[ReconcileDRBDStorageClass] StorageClass with name: " + drbdsc.Name + " not found. Create it.")
		if err = CreateStorageClass(ctx, cl, drbdsc); err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClass] unable to create storage class for DRBDStorageClass resource, name: %s", drbdsc.Name))
			return fmt.Errorf("[ReconcileDRBDStorageClass] error CreateStorageClass: %s", err.Error())
		}
		log.Info("[ReconcileDRBDStorageClass] StorageClass with name: " + drbdsc.Name + " created.")
	} else {
		log.Info("[ReconcileDRBDStorageClass] StorageClass with name: " + drbdsc.Name + " found. Compare it with DRBDStorageClass.")
		equal, msg := CompareDRBDStorageClassAndStorageClass(drbdsc, storageClass)
		if !equal {
			log.Info("[ReconcileDRBDStorageClass] DRBDStorageClass and StorageClass are not equal.")
			drbdsc.Status.Phase = Failed
			drbdsc.Status.Reason = msg

			if err := UpdateDRBDStorageClass(ctx, cl, drbdsc); err != nil {
				log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClass] unable to update DRBD resource, name: %s", drbdsc.Name))
				return fmt.Errorf("[ReconcileDRBDStorageClass] error UpdateDRBDStorageClass: %s", err.Error())
			}
			return nil
		}

		log.Info("[ReconcileDRBDStorageClass] DRBDStorageClass and StorageClass are equal.")
	}

	drbdsc.Status.Phase = Created
	drbdsc.Status.Reason = "DRBDStorageClass and StorageClass are equal."
	if !slices.Contains(drbdsc.ObjectMeta.Finalizers, DRBDStorageClassFinalizerName) {
		drbdsc.ObjectMeta.Finalizers = append(drbdsc.ObjectMeta.Finalizers, DRBDStorageClassFinalizerName)
	}
	err = UpdateDRBDStorageClass(ctx, cl, drbdsc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClass] unable to update DRBD resource, name: %s", drbdsc.Name))
		return fmt.Errorf("error UpdateDRBDStorageClass: %s", err.Error())
	}

	err = LabelNodes(ctx, cl, drbdsc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClass] unable to label nodes for resource, name: %s", drbdsc.Name))
		return fmt.Errorf("error LabelNodes: %s", err.Error())
	}

	if drbdsc.Spec.IsDefault {
		err := makeStorageClassDefault(ctx, cl, drbdsc)
		if err != nil {
			log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClass] unable to make storage class default, name: %s", drbdsc.Name))
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

func ValidateDRBDStorageClass(ctx context.Context, cl client.Client, drbdsc *v1alpha1.DRBDStorageClass, zones map[string]struct{}) (bool, string) {
	var (
		failedMsgBuilder strings.Builder
		validationPassed = true
	)

	failedMsgBuilder.WriteString("Validation of DRBDStorageClass failed: ")

	if drbdsc.Spec.IsDefault {
		storageClass, err := GetStorageClass(ctx, cl, drbdsc.Namespace, drbdsc.Name)
		if err != nil && !errors.IsNotFound(err) {
			validationPassed = false
			failedMsgBuilder.WriteString(fmt.Sprintf("Unable to get StorageClass wit name: %s. Error: %s; ", drbdsc.Name, err.Error()))
		} else {
			isDefault := "false"
			exists := false
			if storageClass != nil {
				isDefault, exists = storageClass.Annotations["storageclass.kubernetes.io/is-default-class"]
			}
			if !exists || isDefault != "true" {
				defaultDRBDSCNames, err := findDefaultDRBDStorageClasses(ctx, cl, drbdsc.Name)
				if err != nil {
					validationPassed = false
					failedMsgBuilder.WriteString(fmt.Sprintf("Unable to find default StoragePool. Error: %s; ", err.Error()))
				} else {
					if len(defaultDRBDSCNames) > 0 {
						validationPassed = false
						failedMsgBuilder.WriteString(fmt.Sprintf("Conflict with other default DRBDStorageClasses: %s; ", strings.Join(defaultDRBDSCNames, ",")))
					}
				}
			}
		}
	}

	if drbdsc.Spec.StoragePool == "" {
		validationPassed = false
		failedMsgBuilder.WriteString("StoragePool is empty; ")
	}

	if drbdsc.Spec.ReclaimPolicy == "" {
		validationPassed = false
		failedMsgBuilder.WriteString("ReclaimPolicy is empty; ")
	}

	switch drbdsc.Spec.Topology {
	case TopologyTransZonal:
		if len(drbdsc.Spec.Zones) == 0 {
			validationPassed = false
			failedMsgBuilder.WriteString("Topology is set to 'TransZonal', but zones are not specified; ")
		} else {
			switch drbdsc.Spec.Replication {
			case ReplicationAvailability, ReplicationConsistencyAndAvailability:
				if len(drbdsc.Spec.Zones) != 3 {
					validationPassed = false
					failedMsgBuilder.WriteString(fmt.Sprintf("Selected unacceptable amount of zones for replication type: %s; correct number of zones should be 3; ", drbdsc.Spec.Replication))
				}
			case ReplicationNone:
			default:
				validationPassed = false
				failedMsgBuilder.WriteString(fmt.Sprintf("Selected unsupported replication type: %s; ", drbdsc.Spec.Replication))
			}
		}
	case TopologyZonal:
		if len(drbdsc.Spec.Zones) != 0 {
			validationPassed = false
			failedMsgBuilder.WriteString("Topology is set to 'Zonal', but zones are specified; ")
		}
	case TopologyIgnored:
		if len(zones) > 0 {
			validationPassed = false
			failedMsgBuilder.WriteString("Setting 'topology' to 'Ignored' is prohibited when zones are present in the cluster; ")
		}
		if len(drbdsc.Spec.Zones) != 0 {
			validationPassed = false
			failedMsgBuilder.WriteString("Topology is set to 'Ignored', but zones are specified; ")
		}
	default:
		validationPassed = false
		failedMsgBuilder.WriteString(fmt.Sprintf("Selected unsupported topology: %s; ", drbdsc.Spec.Topology))
	}

	return validationPassed, failedMsgBuilder.String()
}

func ValidateZonesForExistence(selectedZones []string, clusterZones map[string]struct{}) (bool, []string) {
	wrongZones := make([]string, 0, len(selectedZones))

	for _, zone := range selectedZones {
		if _, exist := clusterZones[zone]; !exist {
			wrongZones = append(wrongZones, zone)
		}
	}

	if len(wrongZones) == 0 {
		return true, wrongZones
	}

	return false, wrongZones
}

func UpdateDRBDStorageClass(ctx context.Context, cl client.Client, drbdsc *v1alpha1.DRBDStorageClass) error {
	err := cl.Update(ctx, drbdsc)
	if err != nil {
		return err
	}
	return nil
}

func CompareDRBDStorageClassAndStorageClass(drbdsc *v1alpha1.DRBDStorageClass, storageClass *storagev1.StorageClass) (bool, string) {
	var (
		failedMsgBuilder strings.Builder
		equal            = true
	)

	failedMsgBuilder.WriteString("DRBDStorageClass and StorageClass are not equal: ")
	newStorageClass := GenerateStorageClassFromDRBDStorageClass(drbdsc)

	if !reflect.DeepEqual(storageClass.Parameters, newStorageClass.Parameters) {
		// TODO: add diff
		equal = false
		failedMsgBuilder.WriteString("Parameters are not equal; ")
	}

	if storageClass.Provisioner != newStorageClass.Provisioner {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("Provisioner are not equal(DRBDStorageClass: %s, StorageClass: %s); ", newStorageClass.Provisioner, storageClass.Provisioner))
	}

	if *storageClass.ReclaimPolicy != *newStorageClass.ReclaimPolicy {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("ReclaimPolicy are not equal(DRBDStorageClass: %s, StorageClass: %s", string(*newStorageClass.ReclaimPolicy), string(*storageClass.ReclaimPolicy)))
	}

	if *storageClass.VolumeBindingMode != *newStorageClass.VolumeBindingMode {
		equal = false
		failedMsgBuilder.WriteString(fmt.Sprintf("VolumeBindingMode are not equal(DRBDStorageClass: %s, StorageClass: %s); ", string(*newStorageClass.VolumeBindingMode), string(*storageClass.VolumeBindingMode)))
	}

	return equal, failedMsgBuilder.String()
}

func CreateStorageClass(ctx context.Context, cl client.Client, drbdsc *v1alpha1.DRBDStorageClass) error {
	newStorageClass := GenerateStorageClassFromDRBDStorageClass(drbdsc)

	err := cl.Create(ctx, newStorageClass)
	if err != nil {
		return err
	}
	return nil
}

func GenerateStorageClassFromDRBDStorageClass(drbdsc *v1alpha1.DRBDStorageClass) *storagev1.StorageClass {
	var allowVolumeExpansion bool = true
	reclaimPolicy := v1.PersistentVolumeReclaimPolicy(drbdsc.Spec.ReclaimPolicy)

	storageClassParameters := map[string]string{
		StorageClassParamFSTypeKey:                     FsTypeExt4,
		StorageClassStoragePoolKey:                     drbdsc.Spec.StoragePool,
		StorageClassParamPlacementPolicyKey:            PlacementPolicyAutoPlaceTopology,
		StorageClassParamNetProtocolKey:                NetProtocolC,
		StorageClassParamNetRRConflictKey:              RrConflictRetryConnect,
		StorageClassParamAutoAddQuorumTieBreakerKey:    "true",
		StorageClassParamOnNoQuorumKey:                 SuspendIo,
		StorageClassParamOnNoDataAccessibleKey:         SuspendIo,
		StorageClassParamOnSuspendedPrimaryOutdatedKey: PrimaryOutdatedForceSecondary,
	}

	switch drbdsc.Spec.Replication {
	case "None":
		storageClassParameters[StorageClassPlacementCountKey] = "1"
		storageClassParameters[StorageClassAutoEvictMinReplicaCountKey] = "1"
		storageClassParameters[StorageClassParamAutoQuorumKey] = SuspendIo
	case "Availability":
		storageClassParameters[StorageClassPlacementCountKey] = "2"
		storageClassParameters[StorageClassAutoEvictMinReplicaCountKey] = "2"
		storageClassParameters[StorageClassParamAutoQuorumKey] = SuspendIo
	case "ConsistencyAndAvailability":
		storageClassParameters[StorageClassPlacementCountKey] = "3"
		storageClassParameters[StorageClassAutoEvictMinReplicaCountKey] = "3"
		storageClassParameters[StorageClassParamAutoQuorumKey] = SuspendIo
	}

	var volumeBindingMode storagev1.VolumeBindingMode
	switch drbdsc.Spec.VolumeAccess {
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

	switch drbdsc.Spec.Topology {
	case TopologyTransZonal:
		storageClassParameters[StorageClassParamReplicasOnSameKey] = fmt.Sprintf("%s/%s", ClassStorageLabel, drbdsc.Name)
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
			Labels:          map[string]string{"storage.deckhouse.io/managed-by": "sds-drbd"},
			Name:            drbdsc.Name,
			Namespace:       drbdsc.Namespace,
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

func LabelNodes(ctx context.Context, cl client.Client, drbdsc *v1alpha1.DRBDStorageClass) error {
	var nodeSelector map[string]string
	allSelectedNodes := map[string]v1.Node{}

	for _, value := range drbdsc.Spec.Zones {
		nodeSelector = map[string]string{ZoneLabel: value}
		selectedNodes := v1.NodeList{}
		err := cl.List(ctx, &selectedNodes, client.MatchingLabels(nodeSelector))
		if err != nil {
			return err
		}
		for _, node := range selectedNodes.Items {
			allSelectedNodes[node.Name] = node
		}
	}

	for _, node := range allSelectedNodes {
		if !labels.Set(node.Labels).Has(fmt.Sprintf("%s/%s", ClassStorageLabel, drbdsc.Name)) {
			node.Labels[fmt.Sprintf("%s/%s", ClassStorageLabel, drbdsc.Name)] = ""
			err := cl.Update(ctx, &node)
			if err != nil {
				return err
			}
		}
	}

	allNodes := v1.NodeList{}
	err := cl.List(ctx, &allNodes)
	if err != nil {
		return err
	}
	for _, node := range allNodes.Items {
		_, exist := allSelectedNodes[node.Name]
		if !exist {
			if labels.Set(node.Labels).Has(fmt.Sprintf("%s/%s", ClassStorageLabel, drbdsc.Name)) {
				delete(node.Labels, fmt.Sprintf("%s/%s", ClassStorageLabel, drbdsc.Name))
				err := cl.Update(ctx, &node)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func findDefaultDRBDStorageClasses(ctx context.Context, cl client.Client, currentDRBDSCName string) ([]string, error) {
	drbdscList := &v1alpha1.DRBDStorageClassList{}
	err := cl.List(ctx, drbdscList)
	if err != nil {
		return nil, err
	}

	var defaultDRBDSCNames []string

	for _, drbdsc := range drbdscList.Items {
		if drbdsc.Name != currentDRBDSCName && drbdsc.Spec.IsDefault {
			defaultDRBDSCNames = append(defaultDRBDSCNames, drbdsc.Name)
		}
	}

	return defaultDRBDSCNames, nil
}

func makeStorageClassDefault(ctx context.Context, cl client.Client, drbdsc *v1alpha1.DRBDStorageClass) error {
	storageClassList := &storagev1.StorageClassList{}
	err := cl.List(ctx, storageClassList)
	if err != nil {
		return err
	}

	for _, sc := range storageClassList.Items {
		_, isDefault := sc.Annotations["storageclass.kubernetes.io/is-default-class"]

		if sc.Name == drbdsc.Name && !isDefault {
			if sc.Annotations == nil {
				sc.Annotations = make(map[string]string)
			}
			sc.Annotations["storageclass.kubernetes.io/is-default-class"] = "true"
		} else if sc.Name != drbdsc.Name && isDefault {
			delete(sc.Annotations, "storageclass.kubernetes.io/is-default-class")
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
