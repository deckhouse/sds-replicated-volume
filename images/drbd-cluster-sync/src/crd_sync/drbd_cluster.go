package crd_sync

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"drbd-cluster-sync/config"

	"k8s.io/client-go/util/retry"

	lsrv "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	lc "github.com/piraeusdatastore/linstor-csi/pkg/linstor/highlevelclient"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ParameterResourceDefQuorumPolicy = "DrbdOptions/Resource/quorum"
	K8sNameLabel                     = "kubernetes.io/metadata.name"
)

type DRBDClusterSyncer struct {
	kc   kubecl.Client
	lc   *lc.HighLevelClient
	opts *config.Options
	log  *log.Entry
}

func NewDRBDClusterSyncer(kc kubecl.Client, lc *lc.HighLevelClient, log *log.Entry, opts *config.Options) *DRBDClusterSyncer {
	return &DRBDClusterSyncer{kc: kc, lc: lc, log: log, opts: opts}
}

func (r *DRBDClusterSyncer) Sync(ctx context.Context) error {
	// Step 1 - get entities from k8s etcd
	DRBDClusters := &srv.DRBDClusterList{}
	if err := r.kc.List(ctx, DRBDClusters); err != nil {
		return fmt.Errorf("failed to get resource definitions: %s", err.Error())
	}

	PVs := &v1.PersistentVolumeList{}
	if err := r.kc.List(ctx, PVs); err != nil {
		return fmt.Errorf("failed to get persistent volumes: %s", err.Error())
	}

	resDefs := &lsrv.ResourceDefinitionsList{}
	if err := r.kc.List(ctx, resDefs); err != nil {
		return fmt.Errorf("failed to get resource definitions: %s", err.Error())
	}

	volDefs := &lsrv.VolumeDefinitionsList{}
	if err := r.kc.List(ctx, volDefs); err != nil {
		return fmt.Errorf("failed to get volume definitions: %s", err.Error())
	}

	resGroups := &lsrv.ResourceGroupsList{}
	if err := r.kc.List(ctx, resGroups); err != nil {
		return fmt.Errorf("failed to get resource groups: %s", err.Error())
	}

	rscList := &srv.ReplicatedStorageClassList{}
	if err := r.kc.List(ctx, rscList); err != nil {
		return fmt.Errorf("failed to get replicated storage class: %s", err.Error())
	}

	layerDRBDResDefList := &lsrv.LayerDrbdResourceDefinitionsList{}
	if err := r.kc.List(ctx, layerDRBDResDefList); err != nil {
		return fmt.Errorf("failed to get layer drbd resource definitions: %s", err.Error())
	}

	// Step 2: Joins and aggregations
	pvToLayerDRBDResDef := make(map[string]*lsrv.LayerDrbdResourceDefinitions, len(layerDRBDResDefList.Items))
	for _, resDef := range layerDRBDResDefList.Items {
		pvToLayerDRBDResDef[strings.ToLower(resDef.Spec.ResourceName)] = &resDef
	}

	PVToRSC := make(map[string]*srv.ReplicatedStorageClass, len(PVs.Items))
	for _, rsc := range rscList.Items {
		PVToRSC[rsc.Name] = &rsc
	}

	PVToVolumeDef := make(map[string]*lsrv.VolumeDefinitions, len(volDefs.Items))
	for _, volDef := range volDefs.Items {
		PVToVolumeDef[strings.ToLower(volDef.Spec.ResourceName)] = &volDef
	}

	ResGrNameToStruct := make(map[string]*lsrv.ResourceGroups, len(resGroups.Items))
	for _, resGroup := range resGroups.Items {
		ResGrNameToStruct[resGroup.Spec.ResourceGroupName] = &resGroup
	}

	RSCNameToStruct := make(map[string]*srv.ReplicatedStorageClass, len(rscList.Items))
	for _, rsc := range rscList.Items {
		RSCNameToStruct[rsc.Name] = &rsc
	}

	PVToResGroup := make(map[string]*lsrv.ResourceGroups, len(resDefs.Items))
	for _, resDef := range resDefs.Items {
		PVToResGroup[strings.ToLower(resDef.Spec.ResourceName)] = ResGrNameToStruct[resDef.Spec.ResourceGroupName]
	}

	PVtoStruct := make(map[string]*v1.PersistentVolume, len(PVs.Items))
	for _, pv := range PVs.Items {
        PVtoStruct[pv.Name] = &pv
    }
	

	ResDefToPV := make(map[string]*v1.PersistentVolume, len(resDefs.Items))
	for _, resDef := range resDefs.Items {
        ResDefToPV[resDef.Name] = PVtoStruct[strings.ToLower(resDef.Spec.ResourceName)]
    }

	// Step 3: Create DRBDCluster
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, r.opts.NumWorkers)
	for _, resDef := range resDefs.Items {
		semaphore <- struct{}{}
		wg.Add(1)
		go createDRBDCluster(ctx, ResDefToPV[resDef.Name], PVToRSC, PVToResGroup, PVToVolumeDef, pvToLayerDRBDResDef, r.log, r.kc, &wg, semaphore, r.opts)
	}

	wg.Wait()
	close(semaphore)
	return nil
}

func createDRBDCluster(
	ctx context.Context,
	pv *v1.PersistentVolume,
	RSCToStruct map[string]*srv.ReplicatedStorageClass,
	PvToResGroup map[string]*lsrv.ResourceGroups,
	PVToVolumeDef map[string]*lsrv.VolumeDefinitions,
	pvToLayerDRBDResDef map[string]*lsrv.LayerDrbdResourceDefinitions,
	log *log.Entry,
	kc kubecl.Client,
	wg *sync.WaitGroup,
	semaphore <-chan struct{},
	opts *config.Options,
) {
	defer func() {
		<-semaphore
	}()
	defer wg.Done()

	autoDiskfulDelaySec := 0
	if RSCToStruct[pv.Spec.StorageClassName].Spec.VolumeAccess == "EventuallyLocal" {
		autoDiskfulDelaySec = 30 * 60
	}

	drbdCluster := &srv.DRBDCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: pv.Name,
		},
		Spec: srv.DRBDClusterSpec{
			Replicas:     int32(PvToResGroup[pv.Name].Spec.ReplicaCount),
			QuorumPolicy: "majority",
			Size:         int64(PVToVolumeDef[pv.Name].Spec.VlmSize),
			SharedSecret: pvToLayerDRBDResDef[pv.Name].Spec.Secret,
			Port:         int32(pvToLayerDRBDResDef[pv.Name].Spec.TCPPort),
			AutoDiskful: srv.AutoDiskful{
				DelaySeconds: autoDiskfulDelaySec,
			},
			StoragePoolSelector: []metav1.LabelSelector{
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      K8sNameLabel,
							Operator: "In",
							Values: []string{
								RSCToStruct[pv.Spec.StorageClassName].Spec.StoragePool,
							},
						},
					},
				},
			},
		},
	}

	// iterate over resdefs
	if err := retry.OnError(
		wait.Backoff{
			Duration: 2 * time.Second,
			Factor:   1.0,
			Steps:    int(opts.RetryCount),
			Cap:      time.Duration(opts.RetryDelaySec),
		},
		func(err error) bool { return true },
		func() error { return kc.Create(ctx, drbdCluster) },
	); err != nil {
		log.Errorf("failed to create a DRBD cluster %s: %s", pv.Name, err.Error())
	}

	log.Infof("DRBD cluster %s successfully created", drbdCluster.ObjectMeta.Name)
}
