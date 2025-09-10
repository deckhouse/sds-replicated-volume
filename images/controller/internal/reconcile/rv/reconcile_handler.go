package rv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type resourceReconcileRequestHandler struct {
	ctx context.Context
	log *slog.Logger
	cl  client.Client
	cfg *ReconcilerClusterConfig
	rv  *v1alpha2.ReplicatedVolume
}

func (h *resourceReconcileRequestHandler) Handle() error {
	h.log.Info("controller: reconcile resource", "name", h.rv.Name)

	// Desired node names
	desiredNodeNames := []string{
		"a-stefurishin-worker-0",
		"a-stefurishin-worker-1",
		"a-stefurishin-worker-2",
	}

	// List all nodes and filter by name
	var nodeList corev1.NodeList
	if err := h.cl.List(h.ctx, &nodeList); err != nil {
		h.log.Error("failed to list Nodes", "error", err)
		return err
	}
	nodes := make([]string, 0, len(desiredNodeNames))
	for _, name := range desiredNodeNames {
		_, found := uiter.Find(
			uslices.Ptrs(nodeList.Items),
			func(n *corev1.Node) bool { return n.Name == name },
		)
		if found {
			nodes = append(nodes, name)
		}
	}
	h.log.Info("fetched nodes (filtered)", "count", len(nodes))

	// Hard-coded LVG names for future use
	lvgNames := []string{
		"placeholder-vg-a",
		"placeholder-vg-b",
	}
	h.log.Info("prepared LVG names", "names", lvgNames)

	// List all LVGs and filter by name
	var lvgList snc.LVMVolumeGroupList
	if err := h.cl.List(h.ctx, &lvgList); err != nil {
		h.log.Error("failed to list LVMVolumeGroups", "error", err)
		return err
	}
	foundLVGs := make(map[string]*snc.LVMVolumeGroup, len(lvgNames))
	for _, name := range lvgNames {
		lvg, found := uiter.Find(
			uslices.Ptrs(lvgList.Items),
			func(x *snc.LVMVolumeGroup) bool { return x.Name == name },
		)
		if found {
			foundLVGs[name] = lvg
		}
	}
	h.log.Info("fetched LVMVolumeGroups (filtered)", "count", len(foundLVGs))

	// Phase 1: query existing/missing
	resCh := make(chan replicaQueryResult, len(nodes))
	var wg sync.WaitGroup
	for _, n := range nodes {
		node := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			resCh <- h.queryReplica(node)
		}()
	}

	go func() { wg.Wait(); close(resCh) }()

	var (
		plans        []replicaInitPlan
		missingPlans []replicaInitPlan
	)
	for res := range resCh {
		switch v := res.(type) {
		case errorReplicaQueryResult:
			return v.Err
		case replicaExists:
			plans = append(plans, replicaInitPlan{Spec: v.RVR.Spec})
			h.log.Info("replica exists", "node", v.Node, "rvr", v.RVR.Name)
		case replicaMissing:
			plan := replicaInitPlan{
				Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: h.rv.Name,
					NodeName:             v.Node,
					NodeId:               0,
					NodeAddress:          v1alpha2.Address{IPv4: "127.0.0.1", Port: v.FreePort},
					Volumes:              []v1alpha2.Volume{{Number: 0, Disk: "/not/used", Device: v.FreeMinor}},
					SharedSecret:         "placeholder",
					Primary:              false,
				},
			}
			plans = append(plans, plan)
			missingPlans = append(missingPlans, plan)
		}
	}

	// Phase 2: initialize missing
	if len(missingPlans) == 0 {
		return nil
	}

	initCh := make(chan replicaInitializationResult, len(missingPlans))
	var iwg sync.WaitGroup
	for _, p := range missingPlans {
		plan := p
		iwg.Add(1)
		go func() {
			defer iwg.Done()
			initCh <- h.initializeReplica(plans, plan)
		}()
	}
	go func() { iwg.Wait(); close(initCh) }()

	for r := range initCh {
		switch v := r.(type) {
		case replicaInitializationError:
			return v.Err
		case replicaInitializationSuccess:
			h.log.Info("replica initialized", "node", v.Node, "rvr", v.RVRName)
		}
	}

	return nil
}

// func (h *resourceReconcileRequestHandler) queryReplicas() (*replicaQueryResult2, error) {
// 	var rvrList v1alpha2.ReplicatedVolumeReplicaList
// 	if err := h.cl.List(
// 		h.ctx,
// 		&rvrList,
// 		client.MatchingFields{"spec.replicatedVolumeName": h.rv.Name},
// 	); err != nil {
// 		return nil, utils.LogError(h.log, fmt.Errorf("getting RVRs by replicatedVolumeName", err))
// 	}

// 	res := &replicaQueryResult2{}
// 	for i, rvr := range rvrList.Items {

// 	}
// }

func (h *resourceReconcileRequestHandler) queryReplica(node string) replicaQueryResult {
	var rvrList v1alpha2.ReplicatedVolumeReplicaList
	if err := h.cl.List(
		h.ctx,
		&rvrList,
		client.MatchingFields{"spec.nodeName": node},
	); err != nil {
		h.log.Error("failed to list RVRs by node", "node", node, "error", err)
		return errorReplicaQueryResult{Node: node, Err: err}
	}

	usedPorts := map[uint]struct{}{}
	usedMinors := map[uint]struct{}{}
	for _, item := range rvrList.Items {
		usedPorts[item.Spec.NodeAddress.Port] = struct{}{}
		for _, v := range item.Spec.Volumes {
			usedMinors[v.Device] = struct{}{}
		}
		if item.Spec.ReplicatedVolumeName == h.rv.Name {
			return replicaExists{Node: node, RVR: &item}
		}
	}

	freePort := findLowestFreePortInRange(usedPorts, 7788, 7799)
	freeMinor := findLowestFreeMinor(usedMinors)
	return replicaMissing{Node: node, FreePort: freePort, FreeMinor: freeMinor}
}

// Phase 2 types

type replicaInitializationResult interface{ _isReplicaInitializationResult() }

type replicaInitializationSuccess struct {
	Node    string
	RVRName string
}

func (replicaInitializationSuccess) _isReplicaInitializationResult() {}

type replicaInitializationError struct {
	Node string
	Err  error
}

func (replicaInitializationError) _isReplicaInitializationResult() {}

type replicaInitPlan struct {
	Spec v1alpha2.ReplicatedVolumeReplicaSpec
}

func (h *resourceReconcileRequestHandler) initializeReplica(all []replicaInitPlan, p replicaInitPlan) replicaInitializationResult {
	rvrPrefix := fmt.Sprintf("%s-%s", h.rv.Name, p.Spec.NodeName)

	peers := map[string]v1alpha2.Peer{}
	for _, other := range all {
		if other.Spec.NodeName == p.Spec.NodeName {
			continue
		}
		peers[other.Spec.NodeName] = v1alpha2.Peer{Address: other.Spec.NodeAddress}
	}

	spec := p.Spec
	spec.Peers = peers

	rvr := &v1alpha2.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", rvrPrefix),
		},
		Spec: spec,
	}

	if err := h.cl.Create(h.ctx, rvr); err != nil {
		h.log.Error("create RVR failed", "node", p.Spec.NodeName, "error", err)
		return replicaInitializationError{Node: p.Spec.NodeName, Err: err}
	}

	createdName := rvr.Name
	if createdName == "" {
		err := errors.New("server did not return created name for generated object")
		h.log.Error("create RVR missing name", "node", p.Spec.NodeName, "error", err)
		return replicaInitializationError{Node: p.Spec.NodeName, Err: err}
	}

	condErr := wait.PollUntilContextTimeout(h.ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		var current v1alpha2.ReplicatedVolumeReplica
		if err := h.cl.Get(ctx, client.ObjectKey{Name: createdName}, &current); err != nil {
			h.log.Error("get RVR failed", "node", p.Spec.NodeName, "name", createdName, "error", err)
			return false, err
		}
		return current.Status != nil &&
				meta.IsStatusConditionTrue(current.Status.Conditions, v1alpha2.ConditionTypeReady),
			nil
	})
	if condErr != nil {
		if wait.Interrupted(condErr) {
			h.log.Error("RVR not ready in time", "node", p.Spec.NodeName, "name", createdName, "error", condErr)
		}
		return replicaInitializationError{Node: p.Spec.NodeName, Err: condErr}
	}

	return replicaInitializationSuccess{Node: p.Spec.NodeName, RVRName: createdName}
}

func findLowestFreePortInRange(used map[uint]struct{}, start, end uint) uint {
	for p := start; p <= end; p++ {
		if _, ok := used[p]; !ok {
			return p
		}
	}
	return 0
}

func findLowestFreeMinor(used map[uint]struct{}) uint {
	for m := uint(0); m <= 1048575; m++ {
		if _, ok := used[m]; !ok {
			return m
		}
	}
	return 0
}
