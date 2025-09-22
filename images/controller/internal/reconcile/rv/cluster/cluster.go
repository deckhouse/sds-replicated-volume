package cluster

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

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
	ByActualNamesOnTheNode(nodeName string, actualVGNameOnTheNode string, actualLVNameOnTheNode string) (*snc.LVMLogicalVolume, error)
}

type Cluster struct {
	ctx          context.Context
	rvrCl        RVRClient
	llvCl        LLVClient
	rvName       string
	sharedSecret string
	// Indexes are node ids.
	replicas []*replica
}

func New(
	ctx context.Context,
	rvrCl RVRClient,
	llvCl LLVClient,
	rvName string,
	sharedSecret string,
) *Cluster {
	return &Cluster{
		ctx:          ctx,
		rvName:       rvName,
		rvrCl:        rvrCl,
		llvCl:        llvCl,
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
		ctx:   c.ctx,
		llvCl: c.llvCl,
		rvrCl: c.rvrCl,
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

	toDelete, toReconcile, toAdd := umaps.IntersectKeys(rvrsByNodeKey, replicasByNodeKey)

	// 0.0. INITIALIZE existing replicas
	for key := range toReconcile {
		rvrs := rvrsByNodeKey[key]

		if len(rvrs) > 1 {
			return nil,
				fmt.Errorf(
					"found duplicate rvrs for rv %s with nodeName %s and nodeId %d: %s",
					c.rvName, key.nodeName, key.nodeId,
					cstrings.JoinNames(rvrs, ", "),
				)
		}

		replica := replicasByNodeKey[key]

		if err := replica.Initialize(rvrs[0]); err != nil {
			return nil, err
		}
		// 0.1. INITIALIZE existing volumes for existing replicas
		if err := replica.InitializeVolumes(); err != nil {
			return nil, err
		}
	}

	// 0.2. INITIALIZE existing volumes for non-existing replicas
	for key := range toAdd {
		replica := replicasByNodeKey[key]
		if err := replica.InitializeVolumes(); err != nil {
			return nil, err
		}
	}

	// 1. RECONCILE - fix or recreate existing replicas
	// This can't be done in parallel, and we should not proceed if some of the
	// correctly placed replicas need to be reconciled, because correct values
	// for spec depend on peers.
	// TODO: But this can be improved by separating reconcile for peer-dependent
	// fields from others.
	toReconcileSorted := slices.Collect(maps.Keys(toReconcile))
	slices.SortFunc(
		toReconcileSorted,
		func(a nodeKey, b nodeKey) int {
			return int(a.nodeId) - int(b.nodeId)
		},
	)
	for _, key := range toReconcileSorted {
		replica := replicasByNodeKey[key]

		replicaAction, err := replica.Reconcile(c.replicas)

		if err != nil {
			return nil, fmt.Errorf("reconciling replica %d: %w", replica.props.id, err)
		}

		if replicaAction != nil {
			return Actions{replicaAction, RetryReconcile{}}, nil
		}
	}

	// 2.0. ADD - create non-existing replicas
	// This also can't be done in parallel, because we need to keep number of
	// active replicas low - and delete one replica as soon as one replica was
	// created
	// TODO: but this can also be improved for the case when no more replicas
	// for deletion has left - then we can parallelize the addition of new replicas
	var rvrsToSkipDelete map[string]struct{}
	var actions Actions
	for id := range toAdd {
		replica := replicasByNodeKey[id]
		replicaAction, err := replica.Create(c.replicas, "")
		if err != nil {
			return nil, fmt.Errorf("initializing replica %d: %w", replica.props.id, err)
		}

		actions = append(actions, replicaAction)

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
	pa := ParallelActions{}

	var deleteErrors error
	for id := range toDelete {
		rvrs := rvrsByNodeKey[id]
		for _, rvr := range rvrs {
			if _, ok := rvrsToSkipDelete[rvr.Name]; ok {
				continue
			}
			deleteAction, err := c.deleteRVR(rvr)

			deleteErrors = errors.Join(deleteErrors, err)

			pa = append(pa, deleteAction)
		}
	}

	actions = append(actions, pa)

	return actions, deleteErrors
}

func (c *Cluster) deleteRVR(rvr *v1alpha2.ReplicatedVolumeReplica) (Action, error) {
	actions := Actions{DeleteReplicatedVolumeReplica{ReplicatedVolumeReplica: rvr}}

	for i := range rvr.Spec.Volumes {
		// expecting: "/dev/{actualVGNameOnTheNode}/{actualLVNameOnTheNode}"
		parts := strings.Split(rvr.Spec.Volumes[i].Disk, "/")
		if len(parts) != 4 || parts[0] != "" || parts[1] != "dev" ||
			len(parts[2]) == 0 || len(parts[3]) == 0 {
			return nil,
				fmt.Errorf(
					"expected rvr.Spec.Volumes[i].Disk in format '/dev/{actualVGNameOnTheNode}/{actualLVNameOnTheNode}', got '%s'",
					rvr.Spec.Volumes[i].Disk,
				)
		}

		actualVGNameOnTheNode, actualLVNameOnTheNode := parts[2], parts[3]

		llv, err := c.llvCl.ByActualNamesOnTheNode(rvr.Spec.NodeName, actualVGNameOnTheNode, actualLVNameOnTheNode)
		if err != nil {
			return nil, err
		}

		if llv != nil {
			actions = append(actions, DeleteLVMLogicalVolume{llv})
		}
	}

	return actions, nil
}
