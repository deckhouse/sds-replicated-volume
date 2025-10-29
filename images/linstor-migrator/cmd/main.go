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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/deckhouse/sds-common-lib/slogh"
	kubeutils "github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/pkg/kubeutils"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	sncv1alpha1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvlinstor "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"

	srvv1alpha2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	csiDriverReplicated     = "replicated.csi.storage.deckhouse.io"
	typeLVMThin             = "Thin"
	typeLVMThick            = "Thick"
	linstorLVMSuffix        = "_00000"
	controllerFinalizerName = "sds-replicated-volume.deckhouse.io/controller"
	llmPhaseCreated         = "Created"

	maximumWaitingTimeInMinutes = 5
)

type linstorDB struct {
	VolumeDefinitions   map[string]srvlinstor.VolumeDefinitions
	ResourceDefinitions map[string]srvlinstor.ResourceDefinitions
	ResourceGroups      map[string]srvlinstor.ResourceGroups
	Resources           map[string][]srvlinstor.Resources
	LayerResourcesIds   *srvlinstor.LayerResourceIdsList
	LayerDrbdResources  map[int]srvlinstor.LayerDrbdResources
	NodeNetInterfaces   *srvlinstor.NodeNetInterfacesList
}

func main() {
	opt := &Opt{}
	opt.Parse()

	ctx := signals.SetupSignalHandler()

	if err := slogh.UpdateConfig(
		slogh.Config{Level: slogh.LevelDebug, Format: slogh.FormatText, Callsite: slogh.CallsiteDisabled},
	); err != nil {
		panic(err)
	}
	logHandler := &slogh.Handler{}
	log := slog.New(logHandler).With("mode", opt.Mode)

	log.Info("linstor-migrator started")
	err := runApp(ctx, log, opt)
	if err != nil {
		log.Error("linstor-migrator exited unexpectedly", "err", err)
		os.Exit(1)
	}

	log.Info("linstor-migrator gracefully shutdown")
	os.Exit(0)
}

func runApp(ctx context.Context, log *slog.Logger, opt *Opt) error {
	scheme, err := newScheme()
	if err != nil {
		return fmt.Errorf("building scheme: %w", err)
	}

	kConfig, err := kubeutils.KubernetesDefaultConfigCreate()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes default config: %w", err)
	}

	kCient, err := kubecl.New(kConfig, kubecl.Options{
		Scheme: scheme,
	})
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	pvs := &corev1.PersistentVolumeList{}
	if err := kCient.List(ctx, pvs); err != nil {
		return fmt.Errorf("failed to get PersistentVolumeList: %w", err)
	}
	if len(pvs.Items) == 0 {
		log.Info("PersistentVolumeList is empty")
		return nil
	}
	replicatedPVs := make([]corev1.PersistentVolume, 0, len(pvs.Items))
	for _, pv := range pvs.Items {
		if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == csiDriverReplicated {
			replicatedPVs = append(replicatedPVs, pv)
		}
	}
	if len(replicatedPVs) == 0 {
		log.Info("Replicated PersistentVolumeList is empty")
		return nil
	}

	if opt.Mode == "get-resources" {
		fmt.Printf("Replicated PersistentVolumeList:\n")
		for i, pv := range replicatedPVs {
			fmt.Printf("%d. %s\n", i+1, pv.Name)
		}
		return nil
	}

	migrationPVs := make([]corev1.PersistentVolume, 0, len(replicatedPVs))
	if len(opt.Resources) > 0 {
		for _, pv := range replicatedPVs {
			if slices.Contains(opt.Resources, pv.Name) {
				migrationPVs = append(migrationPVs, pv)
			}
		}
	} else {
		migrationPVs = replicatedPVs
	}
	if len(migrationPVs) == 0 {
		log.Info("Replicated PersistentVolumeList to migrate is empty")
		return nil
	}
	fmt.Printf("Replicated PersistentVolumeList to migrate:\n")
	for i, pv := range migrationPVs {
		fmt.Printf("%d. %s\n", i+1, pv.Name)
	}

	return runMigrator(ctx, log, kCient, migrationPVs)
}

func runMigrator(ctx context.Context, log *slog.Logger, kCient kubecl.Client, migrationPVs []corev1.PersistentVolume) error {
	log.Info("Migrating Replicated PersistentVolumeList", "number_of_pv", len(migrationPVs))

	// During migration Linstor will be down, so fetch all needed resources from Kubernetes here
	linstorDB, err := initLinstorDB(ctx, kCient)
	if err != nil {
		return fmt.Errorf("failed to initialize linstor DB: %w", err)
	}

	replicatedStoragePools := &srvv1alpha1.ReplicatedStoragePoolList{}
	if err := kCient.List(ctx, replicatedStoragePools); err != nil {
		return fmt.Errorf("failed to get replicated storage pools: %w", err)
	}
	repStorPools := make(map[string]srvv1alpha1.ReplicatedStoragePool)
	for _, pool := range replicatedStoragePools.Items {
		repStorPools[pool.Name] = pool
	}

	// Can be parallelized
	for _, pv := range migrationPVs {
		err := migratePV(ctx, kCient, log, pv, linstorDB, repStorPools)
		if err != nil {
			return fmt.Errorf("failed to migrate PersistentVolume: %w", err)
		}
	}
	return nil
}

func migratePV(
	ctx context.Context,
	kCient kubecl.Client,
	log *slog.Logger,
	pv corev1.PersistentVolume,
	linstorDB *linstorDB,
	repStorPools map[string]srvv1alpha1.ReplicatedStoragePool,
) error {
	log = log.With("pv_name", pv.Name)
	log.Info("Start migrating Replicated PersistentVolume")

	size, err := getPVSize(pv, linstorDB.VolumeDefinitions)
	if err != nil {
		return err
	}
	log.Debug(fmt.Sprintf("size: %d bytes", size))

	poolName, err := getPoolName(pv, linstorDB.ResourceDefinitions, linstorDB.ResourceGroups)
	if err != nil {
		return err
	}
	log.Debug(fmt.Sprintf("pool name: %s", poolName))

	// Fetch all LVMVolumeGroups for this poolName/ReplicatedStoragePool
	myLvmVolumeGroups, err := getMyLvmVolumeGroups(ctx, kCient, log, poolName, repStorPools)
	if err != nil {
		return err
	}

	// TODO:
	rv, err := createOrGetRV(ctx, kCient, log, pv.Name)
	if err != nil {
		return err
	}

	linstorResources, ok := linstorDB.Resources[pv.Name]
	if !ok {
		return fmt.Errorf("linstor Resources not found")
	}
	for _, linstorResource := range linstorResources {
		// 0-Diskful|388-TieBreaker|260-Diskless
		if linstorResource.Spec.ResourceFlags == 0 {
			err := createOrGetLLV(ctx, kCient, log, pv.Name, rv, size, myLvmVolumeGroups, linstorResource)
			if err != nil {
				return err
			}
		}

		err := createOrGetRVR(ctx, kCient, log, pv.Name, rv, myLvmVolumeGroups, linstorResource, linstorDB)
		if err != nil {
			return err
		}
	}

	log.Info("End migrating Replicated PersistentVolume")
	return nil
}

func createOrGetRV(
	ctx context.Context,
	kCient kubecl.Client,
	log *slog.Logger,
	pvName string,
) (*corev1.ConfigMap, error) {
	// TODO: replace ConfigMap -> ReplicatedVolume
	rvName := pvName

	log = log.With("rv_name", rvName)
	log.Info("Creating RV")

	rvExists := &corev1.ConfigMap{}
	err := kCient.Get(ctx, types.NamespacedName{Namespace: "default", Name: rvName}, rvExists)
	if err == nil {
		log.Info("RV already exists")
		return rvExists, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get ReplicatedVolume: %w", err)
	}

	rvNew := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rvName,
			Namespace: "default",
		},
		Data: map[string]string{
			"key": "value",
		},
	}

	err = kCient.Create(ctx, rvNew)
	if err != nil {
		return nil, fmt.Errorf("failed to create ReplicatedVolume: %w", err)
	}

	// TODO: wait ready RV

	log.Info("RV created")

	return rvNew, nil
}

func createOrGetLLV(
	ctx context.Context,
	kCient kubecl.Client,
	log *slog.Logger,
	pvName string,
	rv *corev1.ConfigMap,
	size int,
	myLvmVolumeGroups map[string]sncv1alpha1.LVMVolumeGroup,
	linstorResource srvlinstor.Resources,
) error {
	generateLlvName := fmt.Sprintf("%s%s", pvName, "-")

	log = log.With("node_name", linstorResource.Spec.NodeName, "generate_llv_name", generateLlvName)
	log.Info("Creating LLV")

	var lvmVolumeGroupName string
	var thinPoolName string
	for _, lvg := range myLvmVolumeGroups {
		if strings.ToUpper(lvg.Spec.Local.NodeName) == linstorResource.Spec.NodeName {
			lvmVolumeGroupName = lvg.Name
			if lvg.Spec.ThinPools != nil {
				thinPoolName = lvg.Spec.ThinPools[0].Name
			}
			break
		}
	}
	if lvmVolumeGroupName == "" {
		return fmt.Errorf("LVMVolumeGroup not found")
	}

	llvs := &sncv1alpha1.LVMLogicalVolumeList{}
	if err := kCient.List(ctx, llvs); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get LVMLogicalVolumeList: %w", err)
		}
	}
	for _, llv := range llvs.Items {
		if llv.GenerateName == generateLlvName && llv.Spec.LVMVolumeGroupName == lvmVolumeGroupName {
			if llv.Status.Phase == llmPhaseCreated {
				log.Info("LLV already exists")
				return nil
			} else {
				return fmt.Errorf("LLV already exists but not in phase %s (current phase: %s)", llmPhaseCreated, llv.Status.Phase)
			}
		}
	}

	var typeLVM string
	if thinPoolName != "" {
		typeLVM = typeLVMThin
	} else {
		typeLVM = typeLVMThick
	}

	llvNew := &sncv1alpha1.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: generateLlvName,
			Finalizers:   []string{controllerFinalizerName},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         corev1.SchemeGroupVersion.String(),
					Kind:               "ConfigMap",
					Name:               rv.Name,
					UID:                rv.UID,
					Controller:         ptrTo(true),
					BlockOwnerDeletion: ptrTo(true),
				},
			},
		},
		Spec: sncv1alpha1.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: fmt.Sprintf("%s%s", pvName, linstorLVMSuffix),
			LVMVolumeGroupName:    lvmVolumeGroupName,
			Type:                  typeLVM,
			Size:                  fmt.Sprintf("%d", size),
		},
	}
	if thinPoolName != "" {
		llvNew.Spec.Thin = &sncv1alpha1.LVMLogicalVolumeThinSpec{
			PoolName: thinPoolName,
		}
	}

	err := kCient.Create(ctx, llvNew)
	if err != nil {
		return fmt.Errorf("failed to create LLV: %w", err)
	}

	log = log.With("llv_name", llvNew.Name)

	// Wait for LLV to reach Created phase, polling every 1s, up to maximumWaitingTimeInMinutes minutes
	startTime := time.Now()
	for {
		llvExists := &sncv1alpha1.LVMLogicalVolume{}
		err := kCient.Get(ctx, types.NamespacedName{Namespace: "", Name: llvNew.Name}, llvExists)
		if err != nil {
			return fmt.Errorf("failed to get LLV: %w", err)
		}
		if llvExists.Status != nil && llvExists.Status.Phase == llmPhaseCreated {
			break
		}
		time.Sleep(1 * time.Second)
		if time.Since(startTime) > maximumWaitingTimeInMinutes*time.Minute {
			return fmt.Errorf("LLV created but not in phase %s (current phase: %s)", llmPhaseCreated, llvExists.Status.Phase)
		}
	}
	log.Info(fmt.Sprintf("LLV created and in phase %s", llmPhaseCreated))

	return nil
}

func createOrGetRVR(
	ctx context.Context,
	kCient kubecl.Client,
	log *slog.Logger,
	pvName string,
	rv *corev1.ConfigMap,
	myLvmVolumeGroups map[string]sncv1alpha1.LVMVolumeGroup,
	linstorResource srvlinstor.Resources,
	linstorDB *linstorDB,
) error {
	nodeName := strings.ToLower(linstorResource.Spec.NodeName)
	generateRvrName := fmt.Sprintf("%s-", pvName)

	diskless := false
	if linstorResource.Spec.ResourceFlags != 0 {
		diskless = true
	}

	log = log.With("node_name", nodeName, "generate_rvr_name", generateRvrName, "diskless", fmt.Sprintf("%t", diskless))
	log.Info("Creating RVR")

	rvrs := &srvv1alpha2.ReplicatedVolumeReplicaList{}
	if err := kCient.List(ctx, rvrs); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get ReplicatedVolumeReplicaList: %w", err)
		}
	}
	for _, rvr := range rvrs.Items {
		if rvr.Spec.NodeName == nodeName && rvr.Spec.ReplicatedVolumeName == pvName {
			for _, condition := range rvr.Status.Conditions {
				if condition.Type == srvv1alpha2.ConditionTypeReady && condition.Status == metav1.ConditionTrue {
					log.Info("RVR already exists")
					return nil
				}
			}
		}
	}

	nodeId, err := getNodeId(pvName, nodeName, linstorDB)
	if err != nil {
		return err
	}
	log.Debug(fmt.Sprintf("node id: %d", nodeId))

	//rvrNew := &srvv1alpha2.ReplicatedVolumeReplica{
	//	ObjectMeta: metav1.ObjectMeta{
	//		GenerateName: generateRvrName,
	//		Finalizers:   []string{controllerFinalizerName},
	//		OwnerReferences: []metav1.OwnerReference{
	//			{
	//				APIVersion:         corev1.SchemeGroupVersion.String(),
	//				Kind:               "ConfigMap",
	//				Name:               rv.Name,
	//				UID:                rv.UID,
	//				Controller:         ptrTo(true),
	//				BlockOwnerDeletion: ptrTo(true),
	//			},
	//		},
	//	},
	//	Spec: srvv1alpha2.ReplicatedVolumeReplicaSpec{
	//		NodeName:             nodeName,
	//		ReplicatedVolumeName: pvName,
	//		NodeId:               0,
	//		NodeAddress: srvv1alpha2.Address{
	//			IPv4: "127.0.0.1",
	//			Port: 12345,
	//		},
	//		Peers: map[string]srvv1alpha2.Peer{
	//			"peer1": {
	//				NodeId: 1,
	//				Address: srvv1alpha2.Address{
	//					IPv4: "127.0.0.1",
	//					Port: 12345,
	//				},
	//			},
	//		},
	//		SharedSecret:            "secret",
	//		Primary:                 false,
	//		Quorum:                  0,
	//		QuorumMinimumRedundancy: 0,
	//		AllowTwoPrimaries:       false,
	//		Volumes: []srvv1alpha2.Volume{
	//			{
	//				Device: 0,
	//				Number: 0,
	//				Disk:   "/dev/vg0/demo-11",
	//			},
	//		},
	//	},
	//}

	//err := kCient.Create(ctx, rvrNew)
	//if err != nil {
	//	return fmt.Errorf("failed to create RVR: %w", err)
	//}

	//log = log.With("rvr_name", rvrNew.Name)

	//// Wait for RVR to reach Ready phase, polling every 1s, up to maximumWaitingTimeInMinutes minutes
	//startTime := time.Now()
	//for {
	//	rvrExists := &srvv1alpha2.ReplicatedVolumeReplica{}
	//	err := kCient.Get(ctx, types.NamespacedName{Namespace: "", Name: rvrNew.Name}, rvrExists)
	//	if err != nil {
	//		return fmt.Errorf("failed to get RVR: %w", err)
	//	}
	//	if rvrExists.Status.Phase == rvrPhaseReady {
	//		break
	//	}
	//	time.Sleep(1 * time.Second)
	//	if time.Since(startTime) > maximumWaitingTimeInMinutes*time.Minute {
	//		return fmt.Errorf("RVR created but not in phase %s (current phase: %s)", rvrPhaseReady, rvrExists.Status.Phase)
	//	}
	//}
	//log.Info(fmt.Sprintf("RVR created and in phase %s", rvrPhaseReady))

	return nil
}

func getMyLvmVolumeGroups(
	ctx context.Context,
	kCient kubecl.Client,
	log *slog.Logger,
	poolName string,
	repStorPools map[string]srvv1alpha1.ReplicatedStoragePool,
) (map[string]sncv1alpha1.LVMVolumeGroup, error) {
	log.Debug("Getting my LVMVolumeGroups")

	repStorPool, ok := repStorPools[poolName]
	if !ok {
		return nil, fmt.Errorf("replicated storage pool not found")
	}

	lvmVolumeGroups := make(map[string]sncv1alpha1.LVMVolumeGroup, len(repStorPool.Spec.LVMVolumeGroups))
	for _, rspLvmVolumeGroup := range repStorPool.Spec.LVMVolumeGroups {
		lvg := &sncv1alpha1.LVMVolumeGroup{}
		err := kCient.Get(ctx, types.NamespacedName{Namespace: "", Name: rspLvmVolumeGroup.Name}, lvg)
		if err != nil {
			return nil, fmt.Errorf("failed to get LVMVolumeGroup: %w", err)
		}
		lvmVolumeGroups[rspLvmVolumeGroup.Name] = *lvg
	}

	return lvmVolumeGroups, nil
}

func getPVSize(pv corev1.PersistentVolume, linstorVolDef map[string]srvlinstor.VolumeDefinitions) (int, error) {
	var size int
	if pv.Spec.Capacity != nil {
		if qty, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok {
			size = int(qty.Value())
		}
	}
	if size == 0 {
		volume, ok := linstorVolDef[strings.ToLower(pv.Name)]
		if ok {
			size = volume.Spec.VlmSize * 1024
		}
	}
	if size == 0 {
		return 0, fmt.Errorf("size is 0")
	}
	return size, nil
}

func getPoolName(
	pv corev1.PersistentVolume,
	linstorResDef map[string]srvlinstor.ResourceDefinitions,
	linstorResGr map[string]srvlinstor.ResourceGroups,
) (string, error) {
	resourceDefinition, ok := linstorResDef[strings.ToLower(pv.Name)]
	if !ok {
		return "", fmt.Errorf("linstor resource definition not found")
	}

	resourceGroup, ok := linstorResGr[resourceDefinition.Spec.ResourceGroupName]
	if !ok {
		return "", fmt.Errorf("linstor resource group not found")
	}

	poolNameJson := resourceGroup.Spec.PoolName
	if poolNameJson == "" {
		return "", fmt.Errorf("linstor pool name is empty")
	}

	var poolNames []string
	err := json.Unmarshal([]byte(poolNameJson), &poolNames)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal linstor pool name: %w", err)
	}

	return poolNames[0], nil
}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	var schemeFuncs = []func(s *runtime.Scheme) error{
		corev1.AddToScheme,
		srvv1alpha1.AddToScheme,
		srvv1alpha2.AddToScheme,
		srvlinstor.AddToScheme,
		sncv1alpha1.AddToScheme,
	}

	for i, f := range schemeFuncs {
		if err := f(scheme); err != nil {
			return nil, fmt.Errorf("adding scheme %d: %w", i, err)
		}
	}

	return scheme, nil
}

// TODO https://github.com/deckhouse/sds-common-lib/blob/main/utils/ptr.go
func ptrTo(b bool) *bool {
	return &b
}

func initLinstorDB(ctx context.Context, kCient kubecl.Client) (*linstorDB, error) {
	linstorDB := &linstorDB{}

	volumeDefinitions := &srvlinstor.VolumeDefinitionsList{}
	if err := kCient.List(ctx, volumeDefinitions); err != nil {
		return nil, fmt.Errorf("failed to get linstor VolumeDefinitionsList: %w", err)
	}
	linstorDB.VolumeDefinitions = make(map[string]srvlinstor.VolumeDefinitions)
	for _, volume := range volumeDefinitions.Items {
		linstorDB.VolumeDefinitions[strings.ToLower(volume.Spec.ResourceName)] = volume
	}

	resourceDefinitions := &srvlinstor.ResourceDefinitionsList{}
	if err := kCient.List(ctx, resourceDefinitions); err != nil {
		return nil, fmt.Errorf("failed to get linstor ResourceDefinitionsList: %w", err)
	}
	linstorDB.ResourceDefinitions = make(map[string]srvlinstor.ResourceDefinitions)
	for _, resource := range resourceDefinitions.Items {
		linstorDB.ResourceDefinitions[strings.ToLower(resource.Spec.ResourceName)] = resource
	}

	resourceGroups := &srvlinstor.ResourceGroupsList{}
	if err := kCient.List(ctx, resourceGroups); err != nil {
		return nil, fmt.Errorf("failed to get linstor ResourceGroupsList: %w", err)
	}
	linstorDB.ResourceGroups = make(map[string]srvlinstor.ResourceGroups)
	for _, group := range resourceGroups.Items {
		linstorDB.ResourceGroups[group.Spec.ResourceGroupName] = group
	}

	resources := &srvlinstor.ResourcesList{}
	if err := kCient.List(ctx, resources); err != nil {
		return nil, fmt.Errorf("failed to get linstor ResourcesList: %w", err)
	}
	linstorDB.Resources = make(map[string][]srvlinstor.Resources)
	for _, resource := range resources.Items {
		linstorDB.Resources[strings.ToLower(resource.Spec.ResourceName)] = append(linstorDB.Resources[strings.ToLower(resource.Spec.ResourceName)], resource)
	}

	linstorDB.LayerResourcesIds = &srvlinstor.LayerResourceIdsList{}
	if err := kCient.List(ctx, linstorDB.LayerResourcesIds); err != nil {
		return nil, fmt.Errorf("failed to get linstor LayerResourceIdsList: %w", err)
	}

	layerDrbdResources := &srvlinstor.LayerDrbdResourcesList{}
	if err := kCient.List(ctx, layerDrbdResources); err != nil {
		return nil, fmt.Errorf("failed to get linstor LayerDrbdResourcesList: %w", err)
	}
	linstorDB.LayerDrbdResources = make(map[int]srvlinstor.LayerDrbdResources)
	for _, layerDrbdResource := range layerDrbdResources.Items {
		linstorDB.LayerDrbdResources[layerDrbdResource.Spec.LayerResourceID] = layerDrbdResource
	}

	linstorDB.NodeNetInterfaces = &srvlinstor.NodeNetInterfacesList{}
	if err := kCient.List(ctx, linstorDB.NodeNetInterfaces); err != nil {
		return nil, fmt.Errorf("failed to get linstor NodeNetInterfacesList: %w", err)
	}

	return linstorDB, nil
}

func getNodeId(pvName string, nodeName string, linstorDB *linstorDB) (int, error) {
	fieldLayerResourceId := -1
	for _, layerResourceId := range linstorDB.LayerResourcesIds.Items {
		if layerResourceId.Spec.LayerResourceKind == "DRBD" && strings.EqualFold(layerResourceId.Spec.NodeName, nodeName) && strings.EqualFold(layerResourceId.Spec.ResourceName, pvName) {
			fieldLayerResourceId = layerResourceId.Spec.LayerResourceID
			break
		}
	}
	if fieldLayerResourceId == -1 {
		return -1, fmt.Errorf("field layer_resource_id not found")
	}

	layerDrbdResource, ok := linstorDB.LayerDrbdResources[fieldLayerResourceId]
	if !ok {
		return -1, fmt.Errorf("LayerDrbdResource not found")
	}

	return layerDrbdResource.Spec.NodeID, nil
}
