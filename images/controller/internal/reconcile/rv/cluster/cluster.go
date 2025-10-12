package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

type NodeIdManager interface {
	ReserveNodeId() (uint, error)
}

type PortManager interface {
	// result should not be returned for next calls
	ReserveNodePort(ctx context.Context, nodeName string) (uint, error)
}

type LLVClient interface {
	// return nil, when not found

	ByActualLVNameOnTheNode(ctx context.Context, nodeName string, actualLVNameOnTheNode string) (*snc.LVMLogicalVolume, error)
}

type Cluster struct {
	ctx          context.Context
	log          *slog.Logger
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
	log *slog.Logger,
	rvrCl RVRClient,
	nodeRVRCl NodeRVRClient,
	portRange DRBDPortRange,
	llvCl LLVClient,
	rvName string,
	size int64,
	sharedSecret string,
) *Cluster {
	rm := NewNodeManager(nodeRVRCl, portRange)
	return &Cluster{
		ctx:          ctx,
		log:          log,
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
		log:      c.log.With("replica", nodeName),
		llvCl:    c.llvCl,
		rvrCl:    c.rvrCl,
		portMgr:  c.portManager,
		minorMgr: c.minorManager,
		props: replicaProps{
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

	existingRvrs, err := c.rvrCl.ByReplicatedVolumeName(c.ctx, c.rvName)
	if err != nil {
		return nil, err
	}

	nodeIdMgr := NewExistingRVRManager(existingRvrs)

	rvrsByNodeName := umaps.CollectGrouped(
		uiter.MapTo2(
			uslices.Ptrs(existingRvrs),
			func(rvr *v1alpha2.ReplicatedVolumeReplica) (string, *v1alpha2.ReplicatedVolumeReplica) {
				return rvr.Spec.NodeName, rvr
			},
		),
	)

	replicasByNodeName := maps.Collect(
		uiter.MapTo2(
			slices.Values(c.replicas),
			func(r *Replica) (string, *Replica) {
				return r.props.nodeName, r
			},
		),
	)

	// 0. INITIALIZE existing&new replicas and volumes
	for nodeName, replica := range replicasByNodeName {
		rvrs := rvrsByNodeName[nodeName]

		var rvr *v1alpha2.ReplicatedVolumeReplica
		if len(rvrs) > 1 {
			return nil,
				fmt.Errorf(
					"found duplicate rvrs for rv %s with nodeName %s: %s",
					c.rvName, nodeName,
					cstrings.JoinNames(rvrs, ", "),
				)
		} else if len(rvrs) == 1 {
			rvr = rvrs[0]
		}

		if err := replica.initialize(rvr, c.replicas, nodeIdMgr); err != nil {
			return nil, err
		}
	}

	// Create/Resize all volumes
	pa := ParallelActions{}
	var rvrToResize *v1alpha2.ReplicatedVolumeReplica
	for _, replica := range c.replicas {
		a, resized, err := replica.reconcileVolumes()
		if err != nil {
			return nil, err
		}
		if a != nil {
			pa = append(pa, a)
		}
		if rvrToResize == nil && resized {
			rvrToResize = replica.dprops.existingRVR
		}
	}

	// Diff
	toDelete, toReconcile, toAdd := umaps.IntersectKeys(rvrsByNodeName, replicasByNodeName)

	// 1. RECONCILE - fix or recreate existing replicas
	for key := range toReconcile {
		pa = append(pa, replicasByNodeName[key].recreateOrFix())
	}

	actions := Actions{}
	if len(pa) > 0 {
		actions = append(actions, pa)

		if rvrToResize != nil {
			actions = append(actions, TriggerRVRResize{
				ReplicatedVolumeReplica: rvrToResize,
			})
		}

	} else if len(toAdd)+len(toDelete) == 0 {
		// initial sync
		rvrs := make([]*v1alpha2.ReplicatedVolumeReplica, 0, len(replicasByNodeName))
		for key := range replicasByNodeName {
			rvrs = append(rvrs, rvrsByNodeName[key][0])
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
		replica := replicasByNodeName[id]

		rvr := replica.rvr("")
		actions = append(actions, CreateReplicatedVolumeReplica{rvr}, WaitReplicatedVolumeReplica{rvr})

		// 2.1. DELETE one rvr to alternate addition and deletion
		for id := range toDelete {
			rvrToDelete := rvrsByNodeName[id][0]

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
		rvrs := rvrsByNodeName[id]
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
		_, actualLVNameOnTheNode, err := rvr.Spec.Volumes[i].ParseDisk()
		if err != nil {
			return nil, err
		}

		llv, err := c.llvCl.ByActualLVNameOnTheNode(c.ctx, rvr.Spec.NodeName, actualLVNameOnTheNode)
		if err != nil {
			return nil, err
		}

		if llv != nil {
			actions = append(actions, DeleteLVMLogicalVolume{llv})
		}
	}

	return actions, nil
}
