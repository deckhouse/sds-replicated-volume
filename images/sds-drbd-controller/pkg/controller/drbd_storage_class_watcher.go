package controller

import (
	"context"
	"errors"
	"fmt"
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
	NonOperationalByZonesLabel      = "storage.deckhouse.io/nonOperational-invalid-zones-selected"
	NonOperationalByReplicasLabel   = "storage.deckhouse.io/nonOperational-not-enough-nodes-in-zones"
)

func NewDRBDStorageClassWatcher(
	mgr manager.Manager,
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

			dsps, err := GetAllDRBDStoragePools(ctx, cl)
			if err != nil {
				log.Error(err, "[NewDRBDStorageClassWatcher] unable to get all DRBDStoragePools")
				continue
			}

			nodeList, err := GetAllKubernetesNodes(ctx, cl)
			if err != nil {
				log.Error(err, "[NewDRBDStorageClassWatcher] unable to get all Kubernetes nodes")
				continue
			}

			lvgList, err := GetAllLVMVolumeGroups(ctx, cl)
			if err != nil {
				log.Error(err, "[NewDRBDStorageClassWatcher] unable to get all LVMVolumeGroups")
				continue
			}

			dspZones := GetDRBDStoragePoolsZones(nodeList, lvgList, dsps)

			healthyDSCs := ReconcileDRBDStorageClassZones(ctx, cl, log, dscs, dspZones)
			ReconcileDRBDStorageClassReplication(ctx, cl, log, healthyDSCs, nodeList)

			log.Info("[NewDRBDStorageClassWatcher] ends reconciliation loop")
		}
	}()

	return c, err
}

func GetAllLVMVolumeGroups(ctx context.Context, cl client.Client) (*v1alpha1.LvmVolumeGroupList, error) {
	l := &v1alpha1.LvmVolumeGroupList{}

	err := cl.List(ctx, l)
	if err != nil {
		return nil, err
	}

	return l, nil
}

func ReconcileDRBDStorageClassReplication(
	ctx context.Context,
	cl client.Client,
	log logr.Logger,
	dscs map[string]v1alpha1.DRBDStorageClass,
	nodeList *v1.NodeList,
) {
	log.Info("[ReconcileDRBDStorageClassReplication] starts reconcile")
	zoneNodesCount := make(map[string]int, len(nodeList.Items))
	for _, node := range nodeList.Items {
		if zone, exist := node.Labels[ZoneLabel]; exist {
			zoneNodesCount[zone]++
		}
	}

	for _, dsc := range dscs {
		switch dsc.Spec.Replication {
		case ReplicationNone:
		case ReplicationAvailability, ReplicationConsistencyAndAvailability:
			switch dsc.Spec.Topology {
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
			case TopologyIgnored:
				if len(nodeList.Items) < 3 {
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
				log.Error(err, fmt.Sprintf("unable to validate zones for the DRBDStorageClass %s", dsc.Name))
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

func GetDRBDStoragePoolsZones(nodeList *v1.NodeList, lvgList *v1alpha1.LvmVolumeGroupList, dsps map[string]v1alpha1.DRBDStoragePool) map[string][]string {
	nodes := make(map[string]v1.Node, len(nodeList.Items))
	for _, node := range nodeList.Items {
		nodes[node.Name] = node
	}

	lvgs := make(map[string]v1alpha1.LvmVolumeGroup, len(lvgList.Items))
	for _, lvg := range lvgList.Items {
		lvgs[lvg.Name] = lvg
	}

	lvgZones := make(map[string]map[string]struct{}, len(nodes))
	for _, lvg := range lvgs {
		for _, node := range lvg.Status.Nodes {
			clusterNode := nodes[node.Name]

			if zone, exist := clusterNode.Labels[ZoneLabel]; exist {
				if lvgZones[lvg.Name] == nil {
					lvgZones[lvg.Name] = make(map[string]struct{}, len(nodes))
				}
				lvgZones[lvg.Name][zone] = struct{}{}
			}
		}
	}

	dspZones := make(map[string]map[string]struct{}, len(lvgZones))
	for _, dsp := range dsps {
		for _, lvg := range dsp.Spec.LvmVolumeGroups {
			zones := lvgZones[lvg.Name]

			for zone := range zones {
				if dspZones[dsp.Name] == nil {
					dspZones[dsp.Name] = make(map[string]struct{}, len(zones))
				}
				dspZones[dsp.Name][zone] = struct{}{}
			}
		}
	}

	result := make(map[string][]string, len(dspZones))
	for dsp, zones := range dspZones {
		for zone := range zones {
			result[dsp] = append(result[dsp], zone)
		}
	}

	return result
}

func GetAllDRBDStoragePools(ctx context.Context, cl client.Client) (map[string]v1alpha1.DRBDStoragePool, error) {
	l := &v1alpha1.DRBDStoragePoolList{}

	err := cl.List(ctx, l)
	if err != nil {
		return nil, err
	}

	dsps := make(map[string]v1alpha1.DRBDStoragePool, len(l.Items))
	for _, dsp := range l.Items {
		dsps[dsp.Name] = dsp
	}

	return dsps, nil
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
