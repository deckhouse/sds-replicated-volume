package cluster

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	umaps "github.com/deckhouse/sds-common-lib/utils/maps"
	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	cstrings "github.com/deckhouse/sds-replicated-volume/lib/go/common/strings"
)

type RVRClient interface {
	ByReplicatedVolumeName(ctx context.Context, resourceName string) ([]v1alpha2.ReplicatedVolumeReplica, error)
}

type MinorManager interface {
	// result should not be returned for next calls
	ReserveNodeMinor(ctx context.Context, nodeName string) (uint, error)
}

type PortManager interface {
	// result should not be returned for next calls
	ReserveNodePort(ctx context.Context, nodeName string) (uint, error)
}

type LLVClient interface {
	// return nil, when not found
	ByActualNamesOnTheNode(ctx context.Context, nodeName string, actualVGNameOnTheNode string, actualLVNameOnTheNode string) (*snc.LVMLogicalVolume, error)
}

type Cluster struct {
	ctx          context.Context
	rvrCl        RVRClient
	llvCl        LLVClient
	portManager  PortManager
	minorManager MinorManager
	size         int64
	rvName       string
	sharedSecret string
	// Indexes are node ids.
	replicas []*Replica
}

type ReplicaVolumeOptions struct {
	VGName                string
	ActualVgNameOnTheNode string
	Type                  string
}

func New(
	ctx context.Context,
	rvrCl RVRClient,
	nodeRVRCl NodeRVRClient,
	portRange DRBDPortRange,
	llvCl LLVClient,
	rvName string,
	size int64,
	sharedSecret string,
) *Cluster {
	rm := NewResourceManager(nodeRVRCl, portRange)
	return &Cluster{
		ctx:          ctx,
		rvName:       rvName,
		rvrCl:        rvrCl,
		llvCl:        llvCl,
		portManager:  rm,
		minorManager: rm,
		size:         size,
		sharedSecret: sharedSecret,
	}
}

func (c *Cluster) AddReplica(
	nodeName string,
	ipv4 string,
	primary bool,
	quorum byte,
	quorumMinimumRedundancy byte,
) *Replica {
	r := &Replica{
		ctx:      c.ctx,
		llvCl:    c.llvCl,
		rvrCl:    c.rvrCl,
		portMgr:  c.portManager,
		minorMgr: c.minorManager,
		props: replicaProps{
			id:                      uint(len(c.replicas)),
			rvName:                  c.rvName,
			nodeName:                nodeName,
			ipv4:                    ipv4,
			sharedSecret:            c.sharedSecret,
			primary:                 primary,
			quorum:                  quorum,
			quorumMinimumRedundancy: quorumMinimumRedundancy,
			size:                    c.size,
		},
	}
	c.replicas = append(c.replicas, r)
	return r
}

func (c *Cluster) validateAndNormalize() error {
	// find first replica with non-zero number of volumes
	var expectedVolumeNum int
	for _, r := range c.replicas {
		if expectedVolumeNum = r.volumeNum(); expectedVolumeNum != 0 {
			break
		}
	}

	if expectedVolumeNum == 0 {
		return fmt.Errorf("cluster expected to have at least one replica and one volume")
	}

	// validate same amount of volumes on each replica, or 0
	for i, r := range c.replicas {
		if num := r.volumeNum(); num != 0 && expectedVolumeNum != num {
			return fmt.Errorf(
				"expected to have %d volumes in replica %d on %s, got %d",
				expectedVolumeNum, i, r.props.nodeName, num,
			)
		}
	}

	// for 0-volume replicas create diskless volumes
	for _, r := range c.replicas {
		for r.volumeNum() < expectedVolumeNum {
			r.addVolumeDiskless()
		}
	}

	return nil
}

func (c *Cluster) Reconcile() (Action, error) {
	if err := c.validateAndNormalize(); err != nil {
		return nil, err
	}

	existingRvrs, getErr := c.rvrCl.ByReplicatedVolumeName(c.ctx, c.rvName)
	if getErr != nil {
		return nil, getErr
	}

	type nodeKey struct {
		nodeId   uint
		nodeName string
	}

	rvrsByNodeKey := umaps.CollectGrouped(
		uiter.MapTo2(
			uslices.Ptrs(existingRvrs),
			func(rvr *v1alpha2.ReplicatedVolumeReplica) (nodeKey, *v1alpha2.ReplicatedVolumeReplica) {
				return nodeKey{rvr.Spec.NodeId, rvr.Spec.NodeName}, rvr
			},
		),
	)

	replicasByNodeKey := maps.Collect(
		uiter.MapTo2(
			slices.Values(c.replicas),
			func(r *Replica) (nodeKey, *Replica) {
				return nodeKey{r.props.id, r.props.nodeName}, r
			},
		),
	)

	// 0. INITIALIZE existing&new replicas and volumes
	for key, replica := range replicasByNodeKey {
		rvrs := rvrsByNodeKey[key]

		var rvr *v1alpha2.ReplicatedVolumeReplica
		if len(rvrs) > 1 {
			return nil,
				fmt.Errorf(
					"found duplicate rvrs for rv %s with nodeName %s and nodeId %d: %s",
					c.rvName, key.nodeName, key.nodeId,
					cstrings.JoinNames(rvrs, ", "),
				)
		} else if len(rvrs) == 1 {
			rvr = rvrs[0]
		}

		if err := replica.initialize(rvr, c.replicas); err != nil {
			return nil, err
		}
	}

	// Create/Resize all volumes
	pa := ParallelActions{}
	for _, replica := range c.replicas {
		if a := replica.reconcileVolumes(); a != nil {
			pa = append(pa, a)
		}
	}

	// Diff
	toDelete, toReconcile, toAdd := umaps.IntersectKeys(rvrsByNodeKey, replicasByNodeKey)

	// 1. RECONCILE - fix or recreate existing replicas
	for key := range toReconcile {
		pa = append(pa, replicasByNodeKey[key].recreateOrFix())
	}

	actions := Actions{}
	if len(pa) > 0 {
		actions = append(actions, pa)
	} else if len(toAdd)+len(toDelete) == 0 {
		// initial sync
		rvrs := make([]*v1alpha2.ReplicatedVolumeReplica, 0, len(replicasByNodeKey))
		for key := range replicasByNodeKey {
			rvrs = append(rvrs, rvrsByNodeKey[key][0])
		}
		if len(rvrs) > 0 {
			return WaitAndTriggerInitialSync{rvrs}, nil
		} else {
			return nil, nil
		}
	}

	// 2.0. ADD - create non-existing replicas
	// This can't be done in parallel, because we need to keep number of
	// active replicas low - and delete one replica as soon as one replica was
	// created
	// TODO: but this can also be improved for the case when no more replicas
	// for deletion has left - then we can parallelize the addition of new replicas
	var rvrsToSkipDelete map[string]struct{}
	for id := range toAdd {
		replica := replicasByNodeKey[id]

		rvr := replica.rvr("")
		actions = append(actions, CreateReplicatedVolumeReplica{rvr}, WaitReplicatedVolumeReplica{rvr})

		// 2.1. DELETE one rvr to alternate addition and deletion
		for id := range toDelete {
			rvrToDelete := rvrsByNodeKey[id][0]

			deleteAction, err := c.deleteRVR(rvrToDelete)
			if err != nil {
				return nil, err
			}

			actions = append(actions, deleteAction)

			rvrsToSkipDelete = umaps.Set(rvrsToSkipDelete, rvrToDelete.Name, struct{}{})
			break
		}
	}

	// 3. DELETE not needed RVRs
	deleteActions := ParallelActions{}

	var deleteErrors error
	for id := range toDelete {
		rvrs := rvrsByNodeKey[id]
		for _, rvr := range rvrs {
			if _, ok := rvrsToSkipDelete[rvr.Name]; ok {
				continue
			}
			deleteAction, err := c.deleteRVR(rvr)

			deleteErrors = errors.Join(deleteErrors, err)

			deleteActions = append(deleteActions, deleteAction)
		}
	}

	if len(deleteActions) > 0 {
		actions = append(actions, deleteActions)
	}

	return cleanAction(actions), deleteErrors
}

func (c *Cluster) deleteRVR(rvr *v1alpha2.ReplicatedVolumeReplica) (Action, error) {
	actions := Actions{DeleteReplicatedVolumeReplica{ReplicatedVolumeReplica: rvr}}

	for i := range rvr.Spec.Volumes {
		actualVGNameOnTheNode, actualLVNameOnTheNode, err := rvr.Spec.Volumes[i].ParseDisk()
		if err != nil {
			return nil, err
		}

		llv, err := c.llvCl.ByActualNamesOnTheNode(c.ctx, rvr.Spec.NodeName, actualVGNameOnTheNode, actualLVNameOnTheNode)
		if err != nil {
			return nil, err
		}

		if llv != nil {
			actions = append(actions, DeleteLVMLogicalVolume{llv})
		}
	}

	return actions, nil
}
