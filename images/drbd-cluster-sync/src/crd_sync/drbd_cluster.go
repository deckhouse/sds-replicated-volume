package crd_sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"drbd-cluster-sync/config"

	lsrv "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	srv2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	lc "github.com/piraeusdatastore/linstor-csi/pkg/linstor/highlevelclient"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
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
	pvs := &v1.PersistentVolumeList{}
	if err := r.kc.List(ctx, pvs); err != nil {
		return fmt.Errorf("failed to get persistent volumes: %w", err)
	}

	pvcMap := make(map[string]*v1.PersistentVolume, len(pvs.Items))
	for _, pvc := range pvs.Items {
		pvcMap[pvc.Name] = &pvc
	}

	layerStorageVolumeList := &lsrv.LayerStorageVolumesList{}
	err := r.kc.List(ctx, layerStorageVolumeList)
	if err != nil {
		return fmt.Errorf("failed to list layer storage volumes: %w", err)
	}

	layerStorageResourceIDs := &lsrv.LayerResourceIdsList{}
	err = r.kc.List(ctx, layerStorageResourceIDs)
	if err != nil {
		return fmt.Errorf("failed to list layer resource id: %w", err)
	}

	lriMap := make(map[int]*lsrv.LayerResourceIds, len(layerStorageResourceIDs.Items))
	for _, lri := range layerStorageResourceIDs.Items {
		lriMap[lri.Spec.LayerResourceID] = &lri
	}

	replicaMap := make(map[string]*srv2.DRBDResourceReplica)
	for _, lsv := range layerStorageVolumeList.Items {
		lri, found := lriMap[lsv.Spec.LayerResourceID]
		if !found {
			fmt.Printf("no layer resource id %s found. skipping iteration")
		}

		isDiskless := false
		if lsv.Spec.ProviderKind == "DISKLESS" {
			isDiskless = true
		}
		nodeName := strings.ToLower(lsv.Spec.NodeName)
		
		r, found := replicaMap[lri.Spec.ResourceName]
		if !found {
			pvName := strings.ToLower(lri.Spec.ResourceName)

			replicaMap[lri.Spec.ResourceName] = &srv2.DRBDResourceReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvName,
					Namespace: pvcMap[pvName].Spec.ClaimRef.Namespace,
				},
				Spec: srv2.DRBDResourceReplicaSpec{
					Peers: map[string]srv2.Peer{
						nodeName: srv2.Peer{
							Diskless: isDiskless,
						},
					},
				},
			}
			continue
		}

		peer := r.Spec.Peers[nodeName]
		peer.Diskless = isDiskless
		r.Spec.Peers[nodeName] = peer
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, r.opts.NumWorkers)
	for _, replica := range replicaMap {
		semaphore <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() {
				<-semaphore
				wg.Done()
			}()
			createDRBDResource(ctx, r.kc, replica, r.opts)
		}()
	}

	wg.Wait()
	close(semaphore)
	return nil

	// Step 1 - get entities from k8s etcd
	// drbdClusters := &srv.DRBDClusterList{}
	// if err := r.kc.List(ctx, drbdClusters); err != nil {
	// 	return fmt.Errorf("failed to get resource definitions: %w", err)
	// }

	// pvs := &v1.PersistentVolumeList{}
	// if err := r.kc.List(ctx, pvs); err != nil {
	// 	return fmt.Errorf("failed to get persistent volumes: %w", err)
	// }

	// resDefs := &lsrv.ResourceDefinitionsList{}
	// if err := r.kc.List(ctx, resDefs); err != nil {
	// 	return fmt.Errorf("failed to get resource definitions: %w", err)
	// }

	// volDefs := &lsrv.VolumeDefinitionsList{}
	// if err := r.kc.List(ctx, volDefs); err != nil {
	// 	return fmt.Errorf("failed to get volume definitions: %w", err)
	// }

	// resGroups := &lsrv.ResourceGroupsList{}
	// if err := r.kc.List(ctx, resGroups); err != nil {
	// 	return fmt.Errorf("failed to get resource groups: %w", err)
	// }

	// rscList := &srv.ReplicatedStorageClassList{}
	// if err := r.kc.List(ctx, rscList); err != nil {
	// 	return fmt.Errorf("failed to get replicated storage class: %w", err)
	// }

	// layerDRBDResDefList := &lsrv.LayerDrbdResourceDefinitionsList{}
	// if err := r.kc.List(ctx, layerDRBDResDefList); err != nil {
	// 	return fmt.Errorf("failed to get layer drbd resource definitions: %w", err)
	// }

	// Step 2: Filtering resource definitions
	// drbdClusterFilter := make(map[string]struct{}, len(drbdClusters.Items))
	// for _, cluster := range drbdClusters.Items {
	// 	drbdClusterFilter[strings.ToLower(cluster.Name)] = struct{}{}
	// }

	// filteredResDefs := resDefs.Items[:0]
	// for _, resDef := range resDefs.Items {
	// 	_, clusterExists := drbdClusterFilter[strings.ToLower(resDef.Spec.ResourceName)]

	// 	if !clusterExists {
	// 		filteredResDefs = append(filteredResDefs, resDef)
	// 	}
	// }

	// Step 3: Joins and aggregations
	// pvToLayerDRBDResDef := make(map[string]*lsrv.LayerDrbdResourceDefinitions, len(layerDRBDResDefList.Items))
	// for _, resDef := range layerDRBDResDefList.Items {
	// 	pvToLayerDRBDResDef[strings.ToLower(resDef.Spec.ResourceName)] = &resDef
	// }

	// pvToRSC := make(map[string]*srv.ReplicatedStorageClass, len(pvs.Items))
	// for _, rsc := range rscList.Items {
	// 	pvToRSC[strings.ToLower(rsc.Name)] = &rsc
	// }

	// pvToVolumeDef := make(map[string]*lsrv.VolumeDefinitions, len(volDefs.Items))
	// for _, volDef := range volDefs.Items {
	// 	pvToVolumeDef[strings.ToLower(volDef.Spec.ResourceName)] = &volDef
	// }

	// resGrNameToStruct := make(map[string]*lsrv.ResourceGroups, len(resGroups.Items))
	// for _, resGroup := range resGroups.Items {
	// 	resGrNameToStruct[strings.ToLower(resGroup.Spec.ResourceGroupName)] = &resGroup
	// }

	// rscNameToStruct := make(map[string]*srv.ReplicatedStorageClass, len(rscList.Items))
	// for _, rsc := range rscList.Items {
	// 	rscNameToStruct[strings.ToLower(rsc.Name)] = &rsc
	// }

	// pvToResGroup := make(map[string]*lsrv.ResourceGroups, len(filteredResDefs))
	// for _, resDef := range filteredResDefs {
	// 	pvToResGroup[strings.ToLower(resDef.Spec.ResourceName)] = resGrNameToStruct[strings.ToLower(resDef.Spec.ResourceGroupName)]
	// }

	// pvtoStruct := make(map[string]*v1.PersistentVolume, len(pvs.Items))
	// for _, pv := range pvs.Items {
	// 	pvtoStruct[strings.ToLower(pv.Name)] = &pv
	// }

	// resDefToPV := make(map[string]*v1.PersistentVolume, len(filteredResDefs))
	// for _, resDef := range filteredResDefs {
	// 	resDefToPV[strings.ToLower(resDef.Name)] = pvtoStruct[strings.ToLower(resDef.Spec.ResourceName)]
	// }

	// Step 4: Create DRBDCluster
	// var wg sync.WaitGroup
	// semaphore := make(chan struct{}, r.opts.NumWorkers)
	// for _, resDef := range filteredResDefs {
	// 	semaphore <- struct{}{}
	// 	wg.Add(1)
	// 	go func() {
	// 		defer func() {
	// 			<-semaphore
	// 			wg.Done()
	// 		}()
	// 		createDRBDCluster(ctx, resDefToPV[resDef.Name], pvToRSC, pvToResGroup, pvToVolumeDef, pvToLayerDRBDResDef, r.log, r.kc, r.opts)
	// 	}()
	// }

	// wg.Wait()
	// close(semaphore)
	// return nil
}

func createDRBDResource(ctx context.Context, kc kubecl.Client, drbdResourceReplica *srv2.DRBDResourceReplica, opts *config.Options) {
	if err := retry.OnError(
		// backoff settings
		wait.Backoff{
			Duration: 2 * time.Second,                   // initial delay before first retry
			Factor:   1.0,                               // Cap is multiplied by this value each retry
			Steps:    int(opts.RetryCount),              // amount of retries
			Cap:      time.Duration(opts.RetryDelaySec), // delay between retries
		},
		// this function takes an error returned by kc.Create and decides whether to make a retry or not
		func(err error) bool {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				log.Errorf("drbd resource replica retry context err: %v", err)
				return false
			}

			if statusError, ok := err.(*k8sErr.StatusError); ok {
				switch statusError.ErrStatus.Reason {
				case
					metav1.StatusReasonForbidden,
					metav1.StatusReasonAlreadyExists,
					metav1.StatusReasonInvalid,
					metav1.StatusReasonConflict,
					metav1.StatusReasonBadRequest:
					log.Errorf("drbd resource replica retry creation err: %s", statusError.ErrStatus.Reason)
					return false
				}
			}
			return true
		},
		func() error {
			err := kc.Create(ctx, drbdResourceReplica)
			if err == nil {
				log.Infof("DRBD resource replica %s successfully created", drbdResourceReplica.Name)
			}
			return err
		},
	); err != nil {
		log.Errorf("failed to create a DRBD resource replica %s: %s", drbdResourceReplica.Name, err.Error())
	}
}

func createDRBDCluster(
	ctx context.Context,
	pv *v1.PersistentVolume,
	rscToStruct map[string]*srv.ReplicatedStorageClass,
	pvToResGroup map[string]*lsrv.ResourceGroups,
	pvToVolumeDef map[string]*lsrv.VolumeDefinitions,
	pvToLayerDRBDResDef map[string]*lsrv.LayerDrbdResourceDefinitions,
	log *log.Entry,
	kc kubecl.Client,
	opts *config.Options,
) {
	autoDiskfulDelaySec := 0
	if rscToStruct[pv.Spec.StorageClassName].Spec.VolumeAccess == "EventuallyLocal" {
		autoDiskfulDelaySec = 30 * 60
	}

	drbdCluster := &srv.DRBDCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: pv.Name,
		},
		Spec: srv.DRBDClusterSpec{
			Replicas:     int32(pvToResGroup[pv.Name].Spec.ReplicaCount),
			QuorumPolicy: "majority",
			Size:         int64(pvToVolumeDef[pv.Name].Spec.VlmSize),
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
								rscToStruct[pv.Spec.StorageClassName].Spec.StoragePool,
							},
						},
					},
				},
			},
		},
	}

	if err := retry.OnError(
		// backoff settings
		wait.Backoff{
			Duration: 2 * time.Second,                   // initial delay before first retry
			Factor:   1.0,                               // Cap is multiplied by this value each retry
			Steps:    int(opts.RetryCount),              // amount of retries
			Cap:      time.Duration(opts.RetryDelaySec), // delay between retries
		},
		// this function takes an error returned by kc.Create and decides whether to make a retry or not
		func(err error) bool {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				log.Errorf("drbd cluster retry context err: %v", err)
				return false
			}

			if statusError, ok := err.(*k8sErr.StatusError); ok {
				switch statusError.ErrStatus.Reason {
				case
					metav1.StatusReasonForbidden,
					metav1.StatusReasonAlreadyExists,
					metav1.StatusReasonInvalid,
					metav1.StatusReasonConflict,
					metav1.StatusReasonBadRequest:
					log.Errorf("drbd cluster retry creation err: %s", statusError.ErrStatus.Reason)
					return false
				}
			}
			return true
		},
		func() error {
			err := kc.Create(ctx, drbdCluster)
			if err == nil {
				log.Infof("DRBD cluster %s successfully created", drbdCluster.Name)
			}
			return err
		},
	); err != nil {
		log.Errorf("failed to create a DRBD cluster %s: %s", pv.Name, err.Error())
		return
	}
}
