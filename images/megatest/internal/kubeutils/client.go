/*
Copyright 2026 Flant JSC

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

package kubeutils

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	// informerResyncPeriod is the resync period for shared informers.
	// Normally events arrive instantly via Watch. Resync is a safety net
	// for rare cases (~1%) when Watch connection drops and events are missed.
	// Every resync period, informer re-lists all objects to ensure cache is accurate.
	informerResyncPeriod = 30 * time.Second

	// Informer index keys for efficient lookup by parent resource name.
	indexRVAByRVName = "spec.replicatedVolumeName"
	indexRVRByRVName = "spec.replicatedVolumeName"

	// storageNodeLabel is the label used to identify storage nodes.
	storageNodeLabel = "storage.deckhouse.io/sds-replicated-volume-node"
)

// Client wraps a controller-runtime client with helper methods
type Client struct {
	cl     client.Client
	cfg    *rest.Config
	scheme *runtime.Scheme

	// RV informer with dispatcher for VolumeCheckers.
	// Uses dispatcher pattern instead of per-checker handlers for efficiency:
	// - One handler processes all events (not N handlers for N checkers)
	// - Map lookup O(1) instead of N filter calls per event
	// - Better for 100+ concurrent RV watchers
	rvInformer cache.SharedIndexInformer

	// RVA informer for ReplicatedVolumeAttachment cache.
	// Used by WaitForRVAReady and other attachment-related waits
	// instead of direct GET polling (eliminates millions of API calls).
	// Indexed by spec.replicatedVolumeName for efficient per-RV lookups.
	rvaInformer cache.SharedIndexInformer

	// RVR informer for ReplicatedVolumeReplica cache.
	// Indexed by spec.replicatedVolumeName.
	rvrInformer cache.SharedIndexInformer

	// Node informer for cluster nodes.
	nodeInformer cache.SharedIndexInformer

	informerMu    sync.RWMutex
	informerStop  chan struct{}
	informerReady bool

	// Dispatcher: routes RV events to registered checkers by name.
	// Key: RV name, Value: channel to send updates.
	rvCheckersMu sync.RWMutex
	rvCheckers   map[string]chan *v1alpha1.ReplicatedVolume
}

// NewClient creates a new Kubernetes client
func NewClient() (*Client, error) {
	return NewClientWithKubeconfig("")
}

// NewClientWithKubeconfig creates a new Kubernetes client with the specified kubeconfig path
func NewClientWithKubeconfig(kubeconfigPath string) (*Client, error) {
	var cfg *rest.Config
	var err error

	if kubeconfigPath != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("building config from kubeconfig file %s: %w", kubeconfigPath, err)
		}
	} else {
		cfg, err = config.GetConfig()
		if err != nil {
			return nil, fmt.Errorf("getting kubeconfig: %w", err)
		}
	}

	// Disable rate limiter for megatest to avoid "rate: Wait(n=1) would exceed context deadline" errors.
	// megatest is a test tool that creates/deletes many resources concurrently.
	// In test environments, disabling client-side rate limiting is acceptable.
	// Note: API server may still throttle requests, but client won't block waiting.
	cfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding corev1 to scheme: %w", err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding batchv1 to scheme: %w", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding v1alpha1 to scheme: %w", err)
	}

	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}

	c := &Client{
		cl:           cl,
		cfg:          cfg,
		scheme:       scheme,
		informerStop: make(chan struct{}),
		rvCheckers:   make(map[string]chan *v1alpha1.ReplicatedVolume),
	}

	// Initialize shared informers (RV, RVA)
	if err := c.initInformers(); err != nil {
		return nil, fmt.Errorf("initializing informers: %w", err)
	}

	return c, nil
}

// initInformers creates and starts shared informers for RV and RVA.
// Called once during NewClient(). RV informer also dispatches to VolumeCheckers.
func (c *Client) initInformers() error {
	restCfg := rest.CopyConfig(c.cfg)
	restCfg.GroupVersion = &v1alpha1.SchemeGroupVersion
	restCfg.APIPath = "/apis"
	// Use WithoutConversion() to avoid "no kind X is registered for internal version" errors.
	// CRDs don't have internal versions like core Kubernetes types, so we need to skip
	// version conversion when decoding watch events.
	restCfg.NegotiatedSerializer = serializer.NewCodecFactory(c.scheme).WithoutConversion()

	restClient, err := rest.RESTClientFor(restCfg)
	if err != nil {
		return fmt.Errorf("creating REST client: %w", err)
	}

	// --- RV informer ---
	c.rvInformer = cache.NewSharedIndexInformer(
		crdListWatch(restClient, "replicatedvolumes", &v1alpha1.ReplicatedVolumeList{}),
		&v1alpha1.ReplicatedVolume{},
		informerResyncPeriod,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	// Register single dispatcher handler for VolumeCheckers.
	// More efficient than N handlers for N checkers (O(1) map lookup vs O(N) filter calls).
	_, _ = c.rvInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.dispatchRVEvent(obj)
		},
		UpdateFunc: func(_, newObj interface{}) {
			c.dispatchRVEvent(newObj)
		},
		DeleteFunc: func(_ interface{}) {
			// Delete events are not dispatched - checker stops before RV deletion
		},
	})

	// --- RVA informer (indexed by RV name) ---
	c.rvaInformer = cache.NewSharedIndexInformer(
		crdListWatch(restClient, "replicatedvolumeattachments", &v1alpha1.ReplicatedVolumeAttachmentList{}),
		&v1alpha1.ReplicatedVolumeAttachment{},
		informerResyncPeriod,
		cache.Indexers{
			cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
			indexRVAByRVName: func(obj interface{}) ([]string, error) {
				rva, ok := obj.(*v1alpha1.ReplicatedVolumeAttachment)
				if !ok {
					return nil, nil
				}
				return []string{rva.Spec.ReplicatedVolumeName}, nil
			},
		},
	)

	// --- RVR informer (indexed by RV name) ---
	c.rvrInformer = cache.NewSharedIndexInformer(
		crdListWatch(restClient, "replicatedvolumereplicas", &v1alpha1.ReplicatedVolumeReplicaList{}),
		&v1alpha1.ReplicatedVolumeReplica{},
		informerResyncPeriod,
		cache.Indexers{
			cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
			indexRVRByRVName: func(obj interface{}) ([]string, error) {
				rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
				if !ok {
					return nil, nil
				}
				return []string{rvr.Spec.ReplicatedVolumeName}, nil
			},
		},
	)

	// --- Node informer (filtered by storage label) ---
	coreCfg := rest.CopyConfig(c.cfg)
	coreCfg.GroupVersion = &corev1.SchemeGroupVersion
	coreCfg.APIPath = "/api"
	coreCfg.NegotiatedSerializer = serializer.NewCodecFactory(c.scheme).WithoutConversion()

	coreRestClient, err := rest.RESTClientFor(coreCfg)
	if err != nil {
		return fmt.Errorf("creating core REST client for Node informer: %w", err)
	}

	c.nodeInformer = cache.NewSharedIndexInformer(
		cache.NewFilteredListWatchFromClient(coreRestClient, "nodes", "", func(options *metav1.ListOptions) {
			options.LabelSelector = storageNodeLabel
		}),
		&corev1.Node{},
		informerResyncPeriod,
		cache.Indexers{},
	)

	// Start all informers in background
	go c.rvInformer.Run(c.informerStop)
	go c.rvaInformer.Run(c.informerStop)
	go c.rvrInformer.Run(c.informerStop)
	go c.nodeInformer.Run(c.informerStop)

	// Wait for all caches to sync
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer syncCancel()

	syncDone := make(chan struct{})
	var syncErr error
	go func() {
		if !cache.WaitForCacheSync(c.informerStop,
			c.rvInformer.HasSynced,
			c.rvaInformer.HasSynced,
			c.rvrInformer.HasSynced,
			c.nodeInformer.HasSynced,
		) {
			syncErr = fmt.Errorf("informer cache sync failed")
		}
		close(syncDone)
	}()

	select {
	case <-syncDone:
		if syncErr != nil {
			return syncErr
		}
	case <-syncCtx.Done():
		return fmt.Errorf("timeout waiting for informer cache sync: cluster may be unreachable")
	case <-c.informerStop:
		return fmt.Errorf("informer stopped unexpectedly during initialization")
	}

	c.informerMu.Lock()
	c.informerReady = true
	c.informerMu.Unlock()

	return nil
}

// crdListWatch creates a ListWatch for a cluster-scoped CRD resource.
// listObj is used as the target for list decoding (e.g., &v1alpha1.ReplicatedVolumeList{}).
func crdListWatch(restClient rest.Interface, resource string, listObj runtime.Object) *cache.ListWatch {
	return &cache.ListWatch{
		ListWithContextFunc: func(_ context.Context, options metav1.ListOptions) (runtime.Object, error) {
			result := listObj.DeepCopyObject()
			err := restClient.Get().
				Resource(resource).
				VersionedParams(&options, metav1.ParameterCodec).
				Do(context.Background()).
				Into(result)
			return result, err
		},
		WatchFuncWithContext: func(_ context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.Watch = true
			return restClient.Get().
				Resource(resource).
				VersionedParams(&options, metav1.ParameterCodec).
				Watch(context.Background())
		},
	}
}

// dispatchRVEvent routes an RV event to the registered checker (if any).
// Called by informer handler for Add/Update events.
func (c *Client) dispatchRVEvent(obj interface{}) {
	rv, ok := obj.(*v1alpha1.ReplicatedVolume)
	if !ok {
		return
	}

	c.rvCheckersMu.RLock()
	ch, exists := c.rvCheckers[rv.Name]
	c.rvCheckersMu.RUnlock()

	if exists {
		select {
		case ch <- rv:
		default:
			// Channel full, skip event (checker will get next one or resync)
		}
	}
}

// StopInformers stops all running informers (RV, RVA).
// Called on application shutdown from main.go via defer.
func (c *Client) StopInformers() {
	c.informerMu.Lock()
	defer c.informerMu.Unlock()

	if c.informerReady {
		close(c.informerStop)
		c.informerReady = false
	}
}

// RegisterRVChecker registers a VolumeChecker to receive events for specific RV.
// Returns channel where RV updates will be sent. Caller must call UnregisterRVChecker on shutdown.
// Uses dispatcher pattern: one informer handler routes to many checkers via map lookup.
func (c *Client) RegisterRVChecker(rvName string, ch chan *v1alpha1.ReplicatedVolume) error {
	c.informerMu.RLock()
	ready := c.informerReady
	c.informerMu.RUnlock()

	if !ready {
		return fmt.Errorf("RV informer is not ready")
	}

	c.rvCheckersMu.Lock()
	c.rvCheckers[rvName] = ch
	c.rvCheckersMu.Unlock()

	return nil
}

// UnregisterRVChecker removes a VolumeChecker registration.
// Called by VolumeChecker during shutdown to stop receiving events.
func (c *Client) UnregisterRVChecker(rvName string) {
	c.rvCheckersMu.Lock()
	delete(c.rvCheckers, rvName)
	c.rvCheckersMu.Unlock()
}

// GetRVFromCache gets a ReplicatedVolume from the informer cache by name.
// Returns (nil, nil) if not found — caller must handle this as "not yet created" or "already deleted".
func (c *Client) GetRVFromCache(name string) (*v1alpha1.ReplicatedVolume, error) {
	c.informerMu.RLock()
	defer c.informerMu.RUnlock()

	if !c.informerReady {
		return nil, fmt.Errorf("RV informer is not ready")
	}

	obj, exists, err := c.rvInformer.GetStore().GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	rv, ok := obj.(*v1alpha1.ReplicatedVolume)
	if !ok {
		return nil, fmt.Errorf("unexpected object type in cache: %T", obj)
	}

	return rv, nil
}

// GetRVAFromCache gets a ReplicatedVolumeAttachment from the informer cache by name.
// Returns (nil, nil) if not found.
func (c *Client) GetRVAFromCache(name string) (*v1alpha1.ReplicatedVolumeAttachment, error) {
	c.informerMu.RLock()
	defer c.informerMu.RUnlock()

	if !c.informerReady {
		return nil, fmt.Errorf("RVA informer is not ready")
	}

	obj, exists, err := c.rvaInformer.GetStore().GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	rva, ok := obj.(*v1alpha1.ReplicatedVolumeAttachment)
	if !ok {
		return nil, fmt.Errorf("unexpected object type in cache: %T", obj)
	}

	return rva, nil
}

// Client returns the underlying controller-runtime client
func (c *Client) Client() client.Client {
	return c.cl
}

// GetRandomNodes selects n random unique nodes from the informer cache.
func (c *Client) GetRandomNodes(n int) ([]corev1.Node, error) {
	nodes, err := c.ListNodesFromCache()
	if err != nil {
		return nil, err
	}
	if len(nodes) < n {
		n = len(nodes)
	}

	// Fisher-Yates shuffle and take first n
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	return nodes[:n], nil
}

// ListNodesFromCache returns all storage nodes from the Node informer cache.
func (c *Client) ListNodesFromCache() ([]corev1.Node, error) {
	c.informerMu.RLock()
	defer c.informerMu.RUnlock()

	if !c.informerReady {
		return nil, fmt.Errorf("Node informer is not ready")
	}

	objs := c.nodeInformer.GetStore().List()
	nodes := make([]corev1.Node, 0, len(objs))
	for _, obj := range objs {
		node, ok := obj.(*corev1.Node)
		if !ok {
			continue
		}
		nodes = append(nodes, *node)
	}
	return nodes, nil
}

// CreateRV creates a new ReplicatedVolume
func (c *Client) CreateRV(ctx context.Context, rv *v1alpha1.ReplicatedVolume) error {
	return c.cl.Create(ctx, rv)
}

// DeleteRV deletes a ReplicatedVolume
func (c *Client) DeleteRV(ctx context.Context, rv *v1alpha1.ReplicatedVolume) error {
	return c.cl.Delete(ctx, rv)
}

// GetRV gets a ReplicatedVolume by name directly from the API server (not cache).
// Prefer GetRVFromCache for non-critical reads.
func (c *Client) GetRV(ctx context.Context, name string) (*v1alpha1.ReplicatedVolume, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	err := c.cl.Get(ctx, client.ObjectKey{Name: name}, rv)
	if err != nil {
		return nil, err
	}
	return rv, nil
}

// IsRVReady checks if a ReplicatedVolume has completed initial datamesh formation.
// Ready means: DatameshRevision > 0 and no active Formation transition.
func (c *Client) IsRVReady(rv *v1alpha1.ReplicatedVolume) bool {
	if rv == nil {
		return false
	}
	if rv.Status.DatameshRevision <= 0 {
		return false
	}
	for _, t := range rv.Status.DatameshTransitions {
		if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
			return false
		}
	}
	return true
}

// PatchRV patches a ReplicatedVolume using merge patch strategy
func (c *Client) PatchRV(ctx context.Context, originalRV *v1alpha1.ReplicatedVolume, updatedRV *v1alpha1.ReplicatedVolume) error {
	return c.cl.Patch(ctx, updatedRV, client.MergeFrom(originalRV))
}

func buildRVAName(rvName, nodeName string) string {
	base := "rva-" + rvName + "-" + nodeName
	if len(base) <= 253 {
		return base
	}
	sum := sha1.Sum([]byte(base))
	hash := hex.EncodeToString(sum[:])[:8]
	// "rva-" + rv + "-" + node + "-" + hash
	const prefixLen = 4
	const sepCount = 2
	const hashLen = 8
	maxPartsLen := 253 - prefixLen - sepCount - hashLen
	if maxPartsLen < 2 {
		return "rva-" + hash
	}
	rvMax := maxPartsLen / 2
	nodeMax := maxPartsLen - rvMax
	rvPart := rvName
	if len(rvPart) > rvMax {
		rvPart = rvPart[:rvMax]
	}
	nodePart := nodeName
	if len(nodePart) > nodeMax {
		nodePart = nodePart[:nodeMax]
	}
	return "rva-" + rvPart + "-" + nodePart + "-" + hash
}

// EnsureRVA creates a ReplicatedVolumeAttachment for (rvName, nodeName) if it does not exist.
// Checks informer cache first, falls back to Create.
func (c *Client) EnsureRVA(ctx context.Context, rvName, nodeName string) (*v1alpha1.ReplicatedVolumeAttachment, error) {
	rvaName := buildRVAName(rvName, nodeName)

	existing, err := c.GetRVAFromCache(rvaName)
	if err != nil {
		return nil, fmt.Errorf("get RVA %s from cache: %w", rvaName, err)
	}
	if existing != nil {
		return existing, nil
	}

	rva := &v1alpha1.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvaName,
		},
		Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
			ReplicatedVolumeName: rvName,
			NodeName:             nodeName,
		},
	}
	if err := c.cl.Create(ctx, rva); err != nil {
		return nil, err
	}
	return rva, nil
}

// DeleteRVA deletes a ReplicatedVolumeAttachment for (rvName, nodeName). It is idempotent.
// Uses informer cache for the existence check.
func (c *Client) DeleteRVA(ctx context.Context, rvName, nodeName string) error {
	rvaName := buildRVAName(rvName, nodeName)

	existing, err := c.GetRVAFromCache(rvaName)
	if err != nil {
		return fmt.Errorf("get RVA %s from cache: %w", rvaName, err)
	}
	if existing == nil {
		return nil
	}

	return client.IgnoreNotFound(c.cl.Delete(ctx, existing))
}

// ListRVAsByRVName lists non-deleting RVAs for a given RV from the informer cache.
func (c *Client) ListRVAsByRVName(rvName string) ([]v1alpha1.ReplicatedVolumeAttachment, error) {
	c.informerMu.RLock()
	defer c.informerMu.RUnlock()

	if !c.informerReady {
		return nil, fmt.Errorf("RVA informer is not ready")
	}

	objs, err := c.rvaInformer.GetIndexer().ByIndex(indexRVAByRVName, rvName)
	if err != nil {
		return nil, fmt.Errorf("index lookup for RVA by RV name %q: %w", rvName, err)
	}

	var out []v1alpha1.ReplicatedVolumeAttachment
	for _, obj := range objs {
		rva, ok := obj.(*v1alpha1.ReplicatedVolumeAttachment)
		if !ok {
			continue
		}
		if !rva.DeletionTimestamp.IsZero() {
			continue
		}
		out = append(out, *rva)
	}
	return out, nil
}

// ListRVAsByRVNameDirect lists non-deleting RVAs for a given RV directly from the API server.
// Use only when fresh data is required (e.g., before deletion for accurate timing).
func (c *Client) ListRVAsByRVNameDirect(ctx context.Context, rvName string) ([]v1alpha1.ReplicatedVolumeAttachment, error) {
	list := &v1alpha1.ReplicatedVolumeAttachmentList{}
	if err := c.cl.List(ctx, list); err != nil {
		return nil, err
	}
	var out []v1alpha1.ReplicatedVolumeAttachment
	for _, item := range list.Items {
		if !item.DeletionTimestamp.IsZero() {
			continue
		}
		if item.Spec.ReplicatedVolumeName != rvName {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// WaitForRVAReady waits until RVA Ready condition becomes True.
// Uses the RVA informer cache instead of direct API calls.
func (c *Client) WaitForRVAReady(ctx context.Context, rvName, nodeName string) error {
	rvaName := buildRVAName(rvName, nodeName)
	for {
		rva, err := c.GetRVAFromCache(rvaName)
		if err != nil {
			return err
		}
		if rva == nil {
			if err := waitWithContext(ctx, 500*time.Millisecond); err != nil {
				return err
			}
			continue
		}
		cond := meta.FindStatusCondition(rva.Status.Conditions, v1alpha1.ReplicatedVolumeAttachmentCondReadyType)
		if cond != nil && cond.Status == metav1.ConditionTrue {
			return nil
		}
		// Early exit for permanent attach failures: these are reported via Attached condition reason.
		attachedCond := meta.FindStatusCondition(rva.Status.Conditions, v1alpha1.ReplicatedVolumeAttachmentCondAttachedType)
		if attachedCond != nil &&
			attachedCond.Status == metav1.ConditionFalse &&
			attachedCond.Reason == v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied {
			return fmt.Errorf("RVA %s for volume=%s node=%s not attachable: Attached=%s reason=%s message=%q",
				rvaName, rvName, nodeName, attachedCond.Status, attachedCond.Reason, attachedCond.Message)
		}
		if err := waitWithContext(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
}

// ListRVRsByRVName lists all ReplicatedVolumeReplicas for a given RV from the informer cache.
func (c *Client) ListRVRsByRVName(rvName string) ([]v1alpha1.ReplicatedVolumeReplica, error) {
	c.informerMu.RLock()
	defer c.informerMu.RUnlock()

	if !c.informerReady {
		return nil, fmt.Errorf("RVR informer is not ready")
	}

	objs, err := c.rvrInformer.GetIndexer().ByIndex(indexRVRByRVName, rvName)
	if err != nil {
		return nil, fmt.Errorf("index lookup for RVR by RV name %q: %w", rvName, err)
	}

	result := make([]v1alpha1.ReplicatedVolumeReplica, 0, len(objs))
	for _, obj := range objs {
		rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
		if !ok {
			continue
		}
		result = append(result, *rvr)
	}
	return result, nil
}

// ListRVRsByRVNameDirect lists all ReplicatedVolumeReplicas for a given RV directly from the API server.
// Use only when fresh data is required (e.g., for final health diagnostics).
func (c *Client) ListRVRsByRVNameDirect(ctx context.Context, rvName string) ([]v1alpha1.ReplicatedVolumeReplica, error) {
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	err := c.cl.List(ctx, rvrList)
	if err != nil {
		return nil, err
	}

	var result []v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range rvrList.Items {
		if rvr.Spec.ReplicatedVolumeName == rvName {
			result = append(result, rvr)
		}
	}
	return result, nil
}

// DeleteRVR deletes a ReplicatedVolumeReplica
func (c *Client) DeleteRVR(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica) error {
	return c.cl.Delete(ctx, rvr)
}

// CreateRVR creates a ReplicatedVolumeReplica
func (c *Client) CreateRVR(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica) error {
	return c.cl.Create(ctx, rvr)
}

// ListPods returns pods in namespace matching label selector
func (c *Client) ListPods(ctx context.Context, namespace, labelSelector string) ([]corev1.Pod, error) {
	podList := &corev1.PodList{}

	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, fmt.Errorf("parsing label selector %q: %w", labelSelector, err)
	}

	err = c.cl.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: selector})
	if err != nil {
		return nil, fmt.Errorf("listing pods in namespace %q with selector %q: %w", namespace, labelSelector, err)
	}

	return podList.Items, nil
}

// DeletePod deletes a pod (does not wait for deletion)
func (c *Client) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	return c.cl.Delete(ctx, pod)
}

// waitWithContext waits for the specified duration or until context is cancelled
func waitWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
