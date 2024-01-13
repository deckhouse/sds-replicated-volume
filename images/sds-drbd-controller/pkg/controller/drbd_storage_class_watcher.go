package controller

import (
	"context"
	"errors"
	"fmt"
	lapi "github.com/LINBIT/golinstor/client"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/utils/strings/slices"
	"sds-drbd-controller/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

const (
	DrbdStorageClassWatcherCtrlName = "drbd-storage-class-watcher"
	NonOperationalByStoragePool     = "storage.deckhouse.io/nonOperational-invalid-storage-pool-selected"
	NonOperationalByZonesLabel      = "storage.deckhouse.io/nonOperational-invalid-zones-selected"
	NonOperationalByReplicasLabel   = "storage.deckhouse.io/nonOperational-not-enough-nodes-in-zones"
)

func NewDRBDStorageClassWatcher(
	mgr manager.Manager,
	lc *lapi.Client,
	interval int,
) (controller.Controller, error) {
	cl := mgr.GetClient()
	log := mgr.GetLogger()
	ctx := context.Background()

	c, err := controller.New(DrbdStorageClassWatcherCtrlName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			return reconcile.Result{}, nil
		}),
	})

	if err != nil {
		log.Error(err, fmt.Sprintf("[NewDRBDStorageClassWatcher] unable to create controller, name: %s", DrbdStorageClassWatcherCtrlName))
	}

	go func() {
		for {
			time.Sleep(time.Second * time.Duration(interval))
			log.Info("[NewDRBDStorageClassWatcher] starts reconciliation loop")

			dscs, err := GetAllDRBDStorageClasses(ctx, cl)
			if err != nil {
				log.Error(err, "[NewDRBDStorageClassWatcher] unable to get all DRBDStorageClasses")
				continue
			}

			sps, err := GetAllLinstorStoragePools(ctx, lc)
			if err != nil {
				log.Error(err, "[NewDRBDStorageClassWatcher] unable to get all Linstor Storage Pools")
			}

			nodeList, err := GetAllKubernetesNodes(ctx, cl)
			if err != nil {
				log.Error(err, "[NewDRBDStorageClassWatcher] unable to get all Kubernetes nodes")
			}

			storagePoolsNodes := SortNodesByStoragePool(nodeList, sps)
			dspZones := GetDRBDStoragePoolsZones(storagePoolsNodes)

			healthyDSCs := ReconcileDRBDStorageClassPools(ctx, cl, log, dscs, sps)
			healthyDSCs = ReconcileDRBDStorageClassZones(ctx, cl, log, healthyDSCs, dspZones)
			ReconcileDRBDStorageClassReplication(ctx, cl, log, healthyDSCs, storagePoolsNodes)

			log.Info("[NewDRBDStorageClassWatcher] ends reconciliation loop")
		}
	}()

	return c, err
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

func ReconcileDRBDStorageClassPools(
	ctx context.Context,
	cl client.Client,
	log logr.Logger,
	dscs map[string]v1alpha1.DRBDStorageClass,
	sps map[string][]lapi.StoragePool,
) map[string]v1alpha1.DRBDStorageClass {
	healthy := make(map[string]v1alpha1.DRBDStorageClass, len(dscs))
	for _, dsc := range dscs {
		if _, exist := sps[dsc.Spec.StoragePool]; exist {
			healthy[dsc.Name] = dsc
		} else {
			err := errors.New(fmt.Sprintf("storage pool %s does not exist", dsc.Spec.StoragePool))
			log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClassPools] storage pool validation failed for the DRBDStorageClass %s", dsc.Name))

			setNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, NonOperationalByStoragePool)
		}
	}

	return healthy
}

func ReconcileDRBDStorageClassReplication(
	ctx context.Context,
	cl client.Client,
	log logr.Logger,
	dscs map[string]v1alpha1.DRBDStorageClass,
	spNodes map[string][]v1.Node,
) {
	log.Info("[ReconcileDRBDStorageClassReplication] starts reconcile")

	for _, dsc := range dscs {
		switch dsc.Spec.Replication {
		case ReplicationNone:
		case ReplicationAvailability, ReplicationConsistencyAndAvailability:
			nodes := spNodes[dsc.Spec.StoragePool]
			zoneNodesCount := make(map[string]int, len(nodes))
			for _, node := range nodes {
				if zone, exist := node.Labels[ZoneLabel]; exist {
					zoneNodesCount[zone]++
				}
			}
			switch dsc.Spec.Topology {
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
					log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClassReplication] replicas validation failed for DRBDStorageClass %s", dsc.Name))

					setNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, NonOperationalByReplicasLabel)
				} else {
					removeNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, NonOperationalByReplicasLabel)
				}
				// As we need to place every storage replica in a different zone, we check if at least one node is available in every selected zone.
			case TopologyTransZonal:
				enoughNodes := true
				for _, zone := range dsc.Spec.Zones {
					nodesCount := zoneNodesCount[zone]
					if nodesCount < 1 {
						enoughNodes = false
					}
				}

				if !enoughNodes {
					err := errors.New("not enough nodes are available in the zones for a quorum")
					log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClassReplication] replicas validation failed for DRBDStorageClass %s", dsc.Name))

					setNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, NonOperationalByReplicasLabel)
				} else {
					removeNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, NonOperationalByReplicasLabel)
				}
				// As we do not care about zones, we just check if selected storage pool has enough nodes for quorum.
			case TopologyIgnored:
				if len(spNodes[dsc.Spec.StoragePool]) < 3 {
					err := errors.New("not enough nodes are available in the zones for a quorum")
					log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClassReplication] replicas validation failed for DRBDStorageClass %s", dsc.Name))

					setNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, NonOperationalByReplicasLabel)
				} else {
					removeNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, NonOperationalByReplicasLabel)
				}
			}
		default:
			err := errors.New("unsupported replication type")
			log.Error(err, fmt.Sprintf("[ReconcileDRBDStorageClassReplication] replication type validation failed for DRBDStorageClass %s", dsc.Name))

			setNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, "storage.deckhouse.io/nonOperational")
		}
	}
	log.Info("[ReconcileDRBDStorageClassReplication] ends reconcile")
}

func ReconcileDRBDStorageClassZones(
	ctx context.Context,
	cl client.Client,
	log logr.Logger,
	dscs map[string]v1alpha1.DRBDStorageClass,
	dspZones map[string][]string,
) map[string]v1alpha1.DRBDStorageClass {
	log.Info("[ReconcileDRBDStorageClassZones] starts reconcile")
	healthyDSCs := make(map[string]v1alpha1.DRBDStorageClass, len(dscs))

	for _, dsc := range dscs {
		var (
			healthy = true
			err     error
			zones   = dspZones[dsc.Spec.StoragePool]
		)

		for _, zone := range dsc.Spec.Zones {
			if !slices.Contains(zones, zone) {
				healthy = false
				err = errors.New(fmt.Sprintf("no such zone %s exists in the DRBStoragePool %s", zone, dsc.Spec.StoragePool))
				log.Error(err, fmt.Sprintf("zones validation failed for the DRBDStorageClass %s", dsc.Name))
			}
		}

		if healthy {
			healthyDSCs[dsc.Name] = dsc
			removeNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, NonOperationalByZonesLabel)
		} else {
			setNonOperationalLabelOnStorageClass(ctx, cl, log, dsc, NonOperationalByZonesLabel)
		}
	}
	log.Info("[ReconcileDRBDStorageClassZones] ends reconcile")

	return healthyDSCs
}

func setNonOperationalLabelOnStorageClass(ctx context.Context, cl client.Client, log logr.Logger, dsc v1alpha1.DRBDStorageClass, label string) {
	sc := &storagev1.StorageClass{}

	err := cl.Get(ctx, client.ObjectKey{
		Namespace: dsc.Namespace,
		Name:      dsc.Name,
	}, sc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[setNonOperationalLabelOnStorageClass] unable to get the Kubernetes Storage Class %s", dsc.Name))
		return
	}

	if _, set := sc.Labels[label]; set {
		log.Info(fmt.Sprintf("[setNonOperationalLabelOnStorageClass] a NonOperational label is already set for the Kubernetes Storage Class %s", dsc.Name))
		return
	}

	if sc.Labels == nil {
		sc.Labels = make(map[string]string)
	}

	sc.Labels[label] = ""

	err = cl.Update(ctx, sc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] unable to update the Kubernetes Storage Class %s", dsc.Name))
		return
	}

	log.Info(fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] successfully set a NonOperational label on the Kubernetes Storage Class %s", dsc.Name))
}

func removeNonOperationalLabelOnStorageClass(ctx context.Context, cl client.Client, log logr.Logger, dsc v1alpha1.DRBDStorageClass, label string) {
	sc := &storagev1.StorageClass{}

	err := cl.Get(ctx, client.ObjectKey{
		Namespace: dsc.Namespace,
		Name:      dsc.Name,
	}, sc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] unable to get the Kubernetes Storage Class %s", dsc.Name))
		return
	}

	if _, set := sc.Labels[label]; !set {
		log.Info(fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] a NonOperational label is not set for the Kubernetes Storage Class %s", dsc.Name))
		return
	}

	delete(sc.Labels, label)
	err = cl.Update(ctx, sc)
	if err != nil {
		log.Error(err, fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] unable to update the Kubernetes Storage Class %s", dsc.Name))
		return
	}

	log.Info(fmt.Sprintf("[removeNonOperationalLabelOnStorageClass] successfully removed a NonOperational label from the Kubernetes Storage Class %s", dsc.Name))
}

func GetDRBDStoragePoolsZones(spNodes map[string][]v1.Node) map[string][]string {
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

func GetAllDRBDStorageClasses(ctx context.Context, cl client.Client) (map[string]v1alpha1.DRBDStorageClass, error) {
	l := &v1alpha1.DRBDStorageClassList{}

	err := cl.List(ctx, l)
	if err != nil {
		return nil, err
	}

	dscs := make(map[string]v1alpha1.DRBDStorageClass, len(l.Items))
	for _, dsc := range l.Items {
		dscs[dsc.Name] = dsc
	}

	return dscs, nil
}
