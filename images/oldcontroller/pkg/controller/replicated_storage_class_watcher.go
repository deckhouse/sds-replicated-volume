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
	"errors"
	"fmt"
	"time"

	lapi "github.com/LINBIT/golinstor/client"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-common-lib/logger"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	ReplicatedStorageClassWatcherCtrlName = "replicated-storage-class-watcher"
	NonOperationalByStoragePool           = "storage.deckhouse.io/nonOperational-invalid-storage-pool-selected"
	NonOperationalByZonesLabel            = "storage.deckhouse.io/nonOperational-invalid-zones-selected"
	NonOperationalByReplicasLabel         = "storage.deckhouse.io/nonOperational-not-enough-nodes-in-zones"
	NonOperationalLabel                   = "storage.deckhouse.io/nonOperational"
)

func RunReplicatedStorageClassWatcher(
	mgr manager.Manager,
	lc *lapi.Client,
	interval int,
	log logger.Logger,
) {
	cl := mgr.GetClient()
	ctx := context.Background()

	log.Named("RunReplicatedStorageClassWatcher").Info(fmt.Sprintf("the controller %s starts the work", ReplicatedStorageClassWatcherCtrlName))

	go func() {
		log := log.Named("RunReplicatedStorageClassWatcher")

		for {
			time.Sleep(time.Second * time.Duration(interval))
			log.Info("starts reconciliation loop")

			rscs, err := GetAllReplicatedStorageClasses(ctx, cl)
			if err != nil {
				log.Error("unable to get all ReplicatedStorageClasses", logger.Err(err))
				continue
			}

			sps, err := GetAllLinstorStoragePools(ctx, lc)
			if err != nil {
				log.Error("unable to get all Linstor Storage Pools", logger.Err(err))
			}

			nodeList, err := GetAllKubernetesNodes(ctx, cl)
			if err != nil {
				log.Error("unable to get all Kubernetes nodes", logger.Err(err))
			}

			storagePoolsNodes := SortNodesByStoragePool(nodeList, sps)
			for spName, nodes := range storagePoolsNodes {
				for _, node := range nodes {
					log.Trace(fmt.Sprintf("storage Pool %s has node %s", spName, node.Name))
				}
			}

			rspZones := GetReplicatedStoragePoolsZones(storagePoolsNodes)

			healthyDSCs := ReconcileReplicatedStorageClassPools(ctx, cl, log, rscs, sps)
			healthyDSCs = ReconcileReplicatedStorageClassZones(ctx, cl, log, healthyDSCs, rspZones)
			ReconcileReplicatedStorageClassReplication(ctx, cl, log, healthyDSCs, storagePoolsNodes)

			log.Info("ends reconciliation loop")
		}
	}()
}

func SortNodesByStoragePool(nodeList *v1.NodeList, sps map[string][]lapi.StoragePool) map[string][]v1.Node {
	nodes := make(map[string]v1.Node, len(nodeList.Items))
	for _, node := range nodeList.Items {
		nodes[node.Name] = node
	}

	result := make(map[string][]v1.Node, len(nodes))

	for _, spd := range sps {
		for _, sp := range spd {
			result[sp.StoragePoolName] = append(result[sp.StoragePoolName], nodes[sp.NodeName])
		}
	}

	return result
}

func ReconcileReplicatedStorageClassPools(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	rscs map[string]srv.ReplicatedStorageClass,
	sps map[string][]lapi.StoragePool,
) map[string]srv.ReplicatedStorageClass {
	log = log.Named("ReconcileReplicatedStorageClassPools")

	healthy := make(map[string]srv.ReplicatedStorageClass, len(rscs))
	for _, rsc := range rscs {
		if _, exist := sps[rsc.Spec.StoragePool]; exist {
			healthy[rsc.Name] = rsc

			removeNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByStoragePool)
		} else {
			err := fmt.Errorf("storage pool %s does not exist", rsc.Spec.StoragePool)
			log.Error(fmt.Sprintf("storage pool validation failed for the ReplicatedStorageClass %s", rsc.Name), logger.Err(err))

			setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByStoragePool)
		}
	}

	return healthy
}

func ReconcileReplicatedStorageClassReplication(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	rscs map[string]srv.ReplicatedStorageClass,
	spNodes map[string][]v1.Node,
) {
	log = log.Named("ReconcileReplicatedStorageClassReplication")

	log.Info("starts reconcile")

	for _, rsc := range rscs {
		log.Debug(fmt.Sprintf("ReplicatedStorageClass %s replication type %s", rsc.Name, rsc.Spec.Replication))
		switch rsc.Spec.Replication {
		case ReplicationNone:
		case ReplicationAvailability, ReplicationConsistencyAndAvailability:
			nodes := spNodes[rsc.Spec.StoragePool]
			zoneNodesCount := make(map[string]int, len(nodes))
			for _, node := range nodes {
				if zone, exist := node.Labels[ZoneLabel]; exist {
					zoneNodesCount[zone]++
				}
			}
			log.Debug(fmt.Sprintf("ReplicatedStorageClass %s topology type %s", rsc.Name, rsc.Spec.Topology))
			switch rsc.Spec.Topology {
			// As we need to place 3 storage replicas in a some random zone, we check if at least one zone has enough nodes for quorum.
			case TopologyZonal:
				var enoughNodes bool
				for _, nodesCount := range zoneNodesCount {
					if nodesCount > 2 {
						enoughNodes = true
					}
				}

				if !enoughNodes {
					err := errors.New("not enough nodes in a single zone for a quorum")
					log.Error(fmt.Sprintf("replicas validation failed for ReplicatedStorageClass %s", rsc.Name), logger.Err(err))

					setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				} else {
					removeNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				}
				// As we need to place every storage replica in a different zone, we check if at least one node is available in every selected zone.
			case TopologyTransZonal:
				enoughNodes := true
				for _, zone := range rsc.Spec.Zones {
					nodesCount := zoneNodesCount[zone]
					if nodesCount < 1 {
						enoughNodes = false
					}
				}

				if !enoughNodes {
					err := errors.New("not enough nodes are available in the zones for a quorum")
					log.Error(fmt.Sprintf("replicas validation failed for ReplicatedStorageClass %s", rsc.Name), logger.Err(err))

					setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				} else {
					removeNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				}
				// As we do not care about zones, we just check if selected storage pool has enough nodes for quorum.
			case TopologyIgnored:
				if len(spNodes[rsc.Spec.StoragePool]) < 3 {
					err := errors.New("not enough nodes are available in the zones for a quorum")
					log.Error(fmt.Sprintf("replicas validation failed for ReplicatedStorageClass %s", rsc.Name), logger.Err(err))

					setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				} else {
					removeNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				}
			}
		default:
			err := errors.New("unsupported replication type")
			log.Error(fmt.Sprintf("replication type validation failed for ReplicatedStorageClass %s", rsc.Name), logger.Err(err))

			setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalLabel)
		}
	}
	log.Info("ends reconcile")
}

func ReconcileReplicatedStorageClassZones(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	rscs map[string]srv.ReplicatedStorageClass,
	rspZones map[string][]string,
) map[string]srv.ReplicatedStorageClass {
	log = log.Named("ReconcileReplicatedStorageClassZones")

	log.Info("starts reconcile")
	healthyDSCs := make(map[string]srv.ReplicatedStorageClass, len(rscs))

	for _, rsc := range rscs {
		var (
			healthy = true
			err     error
			zones   = rspZones[rsc.Spec.StoragePool]
		)

		for _, zone := range rsc.Spec.Zones {
			if !slices.Contains(zones, zone) {
				healthy = false
				err = fmt.Errorf("no such zone %s exists in the DRBStoragePool %s", zone, rsc.Spec.StoragePool)
				log.Error(fmt.Sprintf("zones validation failed for the ReplicatedStorageClass %s", rsc.Name), logger.Err(err))
			}
		}

		if healthy {
			healthyDSCs[rsc.Name] = rsc
			removeNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByZonesLabel)
		} else {
			setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByZonesLabel)
		}
	}
	log.Info("ends reconcile")

	return healthyDSCs
}

func setNonOperationalLabelOnStorageClass(ctx context.Context, cl client.Client, log logger.Logger, rsc srv.ReplicatedStorageClass, label string) {
	log = log.Named("setNonOperationalLabelOnStorageClass")

	log = log.Named("removeNonOperationalLabelOnStorageClass")

	sc := &storagev1.StorageClass{}

	err := cl.Get(ctx, client.ObjectKey{
		Namespace: rsc.Namespace,
		Name:      rsc.Name,
	}, sc)
	if err != nil {
		log.Error(fmt.Sprintf("unable to get the Kubernetes Storage Class %s", rsc.Name), logger.Err(err))
		return
	}

	if _, set := sc.Labels[label]; set {
		log.Info(fmt.Sprintf("a NonOperational label is already set for the Kubernetes Storage Class %s", rsc.Name))
		return
	}

	if sc.Labels == nil {
		sc.Labels = make(map[string]string)
	}

	sc.Labels[label] = "true"

	err = cl.Update(ctx, sc)
	if err != nil {
		log.Error(fmt.Sprintf("unable to update the Kubernetes Storage Class %s", rsc.Name), logger.Err(err))
		return
	}

	log.Info(fmt.Sprintf("successfully set a NonOperational label on the Kubernetes Storage Class %s", rsc.Name))
}

func removeNonOperationalLabelOnStorageClass(ctx context.Context, cl client.Client, log logger.Logger, rsc srv.ReplicatedStorageClass, label string) {
	log = log.Named("removeNonOperationalLabelOnStorageClass")

	sc := &storagev1.StorageClass{}

	err := cl.Get(ctx, client.ObjectKey{
		Namespace: rsc.Namespace,
		Name:      rsc.Name,
	}, sc)
	if err != nil {
		log.Error(fmt.Sprintf("unable to get the Kubernetes Storage Class %s", rsc.Name), logger.Err(err))
		return
	}

	if _, set := sc.Labels[label]; !set {
		log.Info(fmt.Sprintf("a NonOperational label is not set for the Kubernetes Storage Class %s", rsc.Name))
		return
	}

	delete(sc.Labels, label)
	err = cl.Update(ctx, sc)
	if err != nil {
		log.Error(fmt.Sprintf("unable to update the Kubernetes Storage Class %s", rsc.Name), logger.Err(err))
		return
	}

	log.Info(fmt.Sprintf("successfully removed a NonOperational label from the Kubernetes Storage Class %s", rsc.Name))
}

func GetReplicatedStoragePoolsZones(spNodes map[string][]v1.Node) map[string][]string {
	spZones := make(map[string]map[string]struct{}, len(spNodes))

	for sp, nodes := range spNodes {
		for _, node := range nodes {
			if zone, exist := node.Labels[ZoneLabel]; exist {
				if spZones[sp] == nil {
					spZones[sp] = make(map[string]struct{}, len(nodes))
				}

				spZones[sp][zone] = struct{}{}
			}
		}
	}

	result := make(map[string][]string, len(spZones))
	for sp, zones := range spZones {
		for zone := range zones {
			result[sp] = append(result[sp], zone)
		}
	}

	return result
}

func GetAllLinstorStoragePools(ctx context.Context, lc *lapi.Client) (map[string][]lapi.StoragePool, error) {
	sps, err := lc.Nodes.GetStoragePoolView(ctx, &lapi.ListOpts{})
	if err != nil {
		return nil, err
	}

	result := make(map[string][]lapi.StoragePool, len(sps))
	for _, sp := range sps {
		result[sp.StoragePoolName] = append(result[sp.StoragePoolName], sp)
	}

	return result, nil
}

func GetAllReplicatedStorageClasses(ctx context.Context, cl client.Client) (map[string]srv.ReplicatedStorageClass, error) {
	l := &srv.ReplicatedStorageClassList{}

	err := cl.List(ctx, l)
	if err != nil {
		return nil, err
	}

	rscs := make(map[string]srv.ReplicatedStorageClass, len(l.Items))
	for _, rsc := range l.Items {
		rscs[rsc.Name] = rsc
	}

	return rscs, nil
}
