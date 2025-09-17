package cluster

import (
	"context"
	"errors"
	"maps"
	"slices"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	umaps "github.com/deckhouse/sds-common-lib/utils/maps"
	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type RVRClient interface {
	ByReplicatedVolumeName(ctx context.Context, resourceName string) ([]v1alpha2.ReplicatedVolumeReplica, error)
	ByNodeName(ctx context.Context, nodeName string) ([]v1alpha2.ReplicatedVolumeReplica, error)
}

type LLVClient interface {
	ByActualNamesOnTheNode(nodeName string, actualVGNameOnTheNode string, actualLVNameOnTheNode string) ([]snc.LVMLogicalVolume, error)
}

type Config interface {
	DRBDPortMinMax() (uint, uint)
}

type Cluster struct {
	ctx    context.Context
	rvrCl  RVRClient
	llvCl  LLVClient
	cfg    Config
	rvName string
	// Indexes are node ids.
	replicas []*replica
}

func New(
	ctx context.Context,
	rvName string,
	rvrCl RVRClient,
	llvCl LLVClient,
) *Cluster {
	return &Cluster{
		ctx:    ctx,
		rvName: rvName,
		rvrCl:  rvrCl,
		llvCl:  llvCl,
	}
}

func (c *Cluster) AddReplica(nodeName string, ipv4 string) *replica {
	r := &replica{
		ctx:      c.ctx,
		llvCl:    c.llvCl,
		rvrCl:    c.rvrCl,
		cfg:      c.cfg,
		id:       len(c.replicas),
		rvName:   c.rvName,
		nodeName: nodeName,
		ipv4:     ipv4,
	}
	c.replicas = append(c.replicas, r)
	return r
}

func (c *Cluster) Reconcile() (res []Action, err error) {
	existingRvrs, err := c.rvrCl.ByReplicatedVolumeName(c.ctx, c.rvName)
	if err != nil {
		return nil, err
	}

	rvrsByNodeId := umaps.CollectGrouped(
		uiter.MapTo2(
			uslices.Ptrs(existingRvrs),
			func(rvr *v1alpha2.ReplicatedVolumeReplica) (int, *v1alpha2.ReplicatedVolumeReplica) {
				return int(rvr.Spec.NodeId), rvr
			},
		),
	)

	replicasByNodeIds := maps.Collect(slices.All(c.replicas))

	toDelete, toReconcile, toAdd := umaps.IntersectKeys(rvrsByNodeId, replicasByNodeIds)

	group := ParallelActionGroup{}

	// 1. RECONCILE
	for id := range toReconcile {
		rvrs := rvrsByNodeId[id]

		replica := replicasByNodeIds[id]

		replicaRes, replicaErr := replica.Reconcile(rvrs)
		group = append(group, replicaRes...)
		err = errors.Join(err, replicaErr)
	}

	// 2. ADD - InitializeSelf
	for id := range toAdd {
		replicaErr := replicasByNodeIds[id].InitializeSelf()
		err = errors.Join(err, replicaErr)
	}

	// 2. ADD - InitializePeers
	// at this point, all replicas are either InitializeSelf'ed or Reconcile'd,
	// so we can finish initialization of peers for new replicas
	for id := range toAdd {
		replica := replicasByNodeIds[id]
		replicaErr := replica.InitializePeers(c.replicas)
		group = append(group, &AddReplica{ReplicatedVolumeReplica: replica.ReplicatedVolumeReplica()})
		err = errors.Join(err, replicaErr)
	}

	res = append(res, group...)

	// 3. DELETE
	for id := range toDelete {
		rvrs := rvrsByNodeId[id]
		for _, rvr := range rvrs {
			res = append(res, &DeleteReplica{ReplicatedVolumeReplica: rvr})
		}
	}

	return
}
