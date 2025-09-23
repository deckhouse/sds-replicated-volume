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
	rvName       string
	sharedSecret string
	// Indexes are node ids.
	replicas []*replica
}

func New(
	ctx context.Context,
	rvrCl RVRClient,
	nodeRVRCl NodeRVRClient,
	portRange DRBDPortRange,
	llvCl LLVClient,
	rvName string,
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
		sharedSecret: sharedSecret,
	}
}

func (c *Cluster) AddReplica(
	nodeName string,
	ipv4 string,
	primary bool,
	quorum byte,
	quorumMinimumRedundancy byte,
) *replica {
	r := &replica{
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
		},
	}
	c.replicas = append(c.replicas, r)
	return r
}

func (c *Cluster) Reconcile() (Action, error) {
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
			func(r *replica) (nodeKey, *replica) {
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

		if err := replica.Initialize(rvr, c.replicas); err != nil {
			return nil, err
		}
	}

	// Create/Resize all volumes
	pa := ParallelActions{}
	for _, replica := range c.replicas {
		pa = append(pa, replica.ReconcileVolumes())
	}

	// Diff
	toDelete, toReconcile, toAdd := umaps.IntersectKeys(rvrsByNodeKey, replicasByNodeKey)

	// 1. RECONCILE - fix or recreate existing replicas
	for key := range toReconcile {
		pa = append(pa, replicasByNodeKey[key].RecreateOrFix())
	}

	actions := Actions{pa}

	// 2.0. ADD - create non-existing replicas
	// This also can't be done in parallel, because we need to keep number of
	// active replicas low - and delete one replica as soon as one replica was
	// created
	// TODO: but this can also be improved for the case when no more replicas
	// for deletion has left - then we can parallelize the addition of new replicas
	var rvrsToSkipDelete map[string]struct{}
	for id := range toAdd {
		replica := replicasByNodeKey[id]

		rvr := replica.RVR("")
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

	actions = append(actions, deleteActions)

	return actions, deleteErrors
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
