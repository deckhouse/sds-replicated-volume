package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	lapi "github.com/LINBIT/golinstor/client"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"sds-replicated-volume-controller/pkg/logger"
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

	log.Info(fmt.Sprintf("[RunReplicatedStorageClassWatcher] the controller %s starts the work", ReplicatedStorageClassWatcherCtrlName))

	go func() {
		for {
			time.Sleep(time.Second * time.Duration(interval))
			log.Info("[RunReplicatedStorageClassWatcher] starts reconciliation loop")

			rscs, err := GetAllReplicatedStorageClasses(ctx, cl)
			if err != nil {
				log.Error(err, "[RunReplicatedStorageClassWatcher] unable to get all ReplicatedStorageClasses")
				continue
			}

			sps, err := GetAllLinstorStoragePools(ctx, lc)
			if err != nil {
				log.Error(err, "[RunReplicatedStorageClassWatcher] unable to get all Linstor Storage Pools")
			}

			nodeList, err := GetAllKubernetesNodes(ctx, cl)
			if err != nil {
				log.Error(err, "[RunReplicatedStorageClassWatcher] unable to get all Kubernetes nodes")
			}

			storagePoolsNodes := SortNodesByStoragePool(nodeList, sps)
			for spName, nodes := range storagePoolsNodes {
				for _, node := range nodes {
					log.Trace(fmt.Sprintf("[RunReplicatedStorageClassWatcher] Storage Pool %s has node %s", spName, node.Name))
				}
			}

			rspZones := GetReplicatedStoragePoolsZones(storagePoolsNodes)

			healthyDSCs := ReconcileReplicatedStorageClassPools(ctx, cl, log, rscs, sps)
			healthyDSCs = ReconcileReplicatedStorageClassZones(ctx, cl, log, healthyDSCs, rspZones)
			ReconcileReplicatedStorageClassReplication(ctx, cl, log, healthyDSCs, storagePoolsNodes)

			log.Info("[RunReplicatedStorageClassWatcher] ends reconciliation loop")
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
	healthy := make(map[string]srv.ReplicatedStorageClass, len(rscs))
	for _, rsc := range rscs {
		if _, exist := sps[rsc.Spec.StoragePool]; exist {
			healthy[rsc.Name] = rsc

			removeNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByStoragePool)
		} else {
			err := fmt.Errorf("storage pool %s does not exist", rsc.Spec.StoragePool)
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClassPools] storage pool validation failed for the ReplicatedStorageClass %s", rsc.Name))

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
	log.Info("[ReconcileReplicatedStorageClassReplication] starts reconcile")

	for _, rsc := range rscs {
		log.Debug(fmt.Sprintf("[ReconcileReplicatedStorageClassReplication] ReplicatedStorageClass %s replication type %s", rsc.Name, rsc.Spec.Replication))
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
			log.Debug(fmt.Sprintf("[ReconcileReplicatedStorageClassReplication] ReplicatedStorageClass %s topology type %s", rsc.Name, rsc.Spec.Topology))
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
					log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClassReplication] replicas validation failed for ReplicatedStorageClass %s", rsc.Name))

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
					log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClassReplication] replicas validation failed for ReplicatedStorageClass %s", rsc.Name))

					setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				} else {
					removeNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				}
				// As we do not care about zones, we just check if selected storage pool has enough nodes for quorum.
			case TopologyIgnored:
				if len(spNodes[rsc.Spec.StoragePool]) < 3 {
					err := errors.New("not enough nodes are available in the zones for a quorum")
					log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClassReplication] replicas validation failed for ReplicatedStorageClass %s", rsc.Name))

					setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				} else {
					removeNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByReplicasLabel)
				}
			}
		default:
			err := errors.New("unsupported replication type")
			log.Error(err, fmt.Sprintf("[ReconcileReplicatedStorageClassReplication] replication type validation failed for ReplicatedStorageClass %s", rsc.Name))

			setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalLabel)
		}
	}
	log.Info("[ReconcileReplicatedStorageClassReplication] ends reconcile")
}

func ReconcileReplicatedStorageClassZones(
	ctx context.Context,
	cl client.Client,
	log logger.Logger,
	rscs map[string]srv.ReplicatedStorageClass,
	rspZones map[string][]string,
) map[string]srv.ReplicatedStorageClass {
	log.Info("[ReconcileReplicatedStorageClassZones] starts reconcile")
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
				log.Error(err, fmt.Sprintf("zones validation failed for the ReplicatedStorageClass %s", rsc.Name))
			}
		}

		if healthy {
			healthyDSCs[rsc.Name] = rsc
			removeNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByZonesLabel)
		} else {
			setNonOperationalLabelOnStorageClass(ctx, cl, log, rsc, NonOperationalByZonesLabel)
		}
	}
	log.Info("[ReconcileReplicatedStorageClassZones] ends reconcile")

	return healthyDSCs
}

func setNonOperationalLabelOnStorageClass(ctx context.Context, cl client.Client, log logger.Logger, rsc srv.ReplicatedStorageClass, label string) {
	sc := &storagev1.StorageClass{}

	err := cl.Get(ctx, client.ObjectKey{
		Namespace: rsc.Namespace,
		Name:      rsc.Name,
	}, sc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[setNonOperationalLabelOnStorageClass] unable to get the Kubernetes Storage Class %s", rsc.Name))
		return
	}

	if _, set := sc.Labels[label]; set {
		log.Info(fmt.Sprintf("[setNonOperationalLabelOnStorageClass] a NonOperational label is already set for the Kubernetes Storage Class %s", rsc.Name))
		return
	}

	if sc.Labels == nil {
		sc.Labels = make(map[string]string)
	}

	sc.Labels[label] = "true"

	err = cl.Update(ctx, sc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] unable to update the Kubernetes Storage Class %s", rsc.Name))
		return
	}

	log.Info(fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] successfully set a NonOperational label on the Kubernetes Storage Class %s", rsc.Name))
}

func removeNonOperationalLabelOnStorageClass(ctx context.Context, cl client.Client, log logger.Logger, rsc srv.ReplicatedStorageClass, label string) {
	sc := &storagev1.StorageClass{}

	err := cl.Get(ctx, client.ObjectKey{
		Namespace: rsc.Namespace,
		Name:      rsc.Name,
	}, sc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] unable to get the Kubernetes Storage Class %s", rsc.Name))
		return
	}

	if _, set := sc.Labels[label]; !set {
		log.Info(fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] a NonOperational label is not set for the Kubernetes Storage Class %s", rsc.Name))
		return
	}

	delete(sc.Labels, label)
	err = cl.Update(ctx, sc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] unable to update the Kubernetes Storage Class %s", rsc.Name))
		return
	}

	log.Info(fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] successfully removed a NonOperational label from the Kubernetes Storage Class %s", rsc.Name))
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
