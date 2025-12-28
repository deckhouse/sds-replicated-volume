/*
Copyright 2025 Flant JSC

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
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

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
	// rvInformerResyncPeriod is the resync period for the RV informer.
	// Normally events arrive instantly via Watch. Resync is a safety net
	// for rare cases (~1%) when Watch connection drops and events are missed.
	// Every resync period, informer re-lists all RVs to ensure cache is accurate.
	rvInformerResyncPeriod = 30 * time.Second

	// nodesCacheTTL is the time-to-live for the nodes cache.
	nodesCacheTTL = 30 * time.Second
)

// Client wraps a controller-runtime client with helper methods
type Client struct {
	cl     client.Client
	cfg    *rest.Config
	scheme *runtime.Scheme

	// Cached nodes with TTL
	cachedNodes    []corev1.Node
	nodesCacheTime time.Time
	nodesMutex     sync.RWMutex

	// RV informer with dispatcher for VolumeCheckers.
	// Uses dispatcher pattern instead of per-checker handlers for efficiency:
	// - One handler processes all events (not N handlers for N checkers)
	// - Map lookup O(1) instead of N filter calls per event
	// - Better for 100+ concurrent RV watchers
	rvInformer    cache.SharedIndexInformer
	rvInformerMu  sync.RWMutex
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

	// Initialize RV informer
	if err := c.initRVInformer(); err != nil {
		return nil, fmt.Errorf("initializing RV informer: %w", err)
	}

	return c, nil
}

// initRVInformer creates and starts the shared informer for ReplicatedVolumes.
// Called once during NewClient(). VolumeCheckers register handlers via AddRVEventHandler().
func (c *Client) initRVInformer() error {
	// Create REST client for RV
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

	// Create ListWatch for ReplicatedVolumes using REST client methods directly
	lw := &cache.ListWatch{
		ListWithContextFunc: func(_ context.Context, options metav1.ListOptions) (runtime.Object, error) {
			result := &v1alpha1.ReplicatedVolumeList{}
			err := restClient.Get().
				Resource("replicatedvolumes").
				VersionedParams(&options, metav1.ParameterCodec).
				Do(context.Background()).
				Into(result)
			return result, err
		},
		WatchFuncWithContext: func(_ context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.Watch = true
			return restClient.Get().
				Resource("replicatedvolumes").
				VersionedParams(&options, metav1.ParameterCodec).
				Watch(context.Background())
		},
	}

	// Create shared informer
	c.rvInformer = cache.NewSharedIndexInformer(
		lw,
		&v1alpha1.ReplicatedVolume{},
		rvInformerResyncPeriod,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	// Register single dispatcher handler.
	// This handler routes events to registered checkers by RV name.
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

	// Start informer in background
	go c.rvInformer.Run(c.informerStop)

	// Wait for cache sync with timeout to detect connectivity issues early
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer syncCancel()

	syncDone := make(chan struct{})
	var syncErr error
	go func() {
		// This is a blocking call that waits for the cache to be synced or the context is cancelled
		if !cache.WaitForCacheSync(c.informerStop, c.rvInformer.HasSynced) {
			syncErr = fmt.Errorf("cache sync failed")
		}
		close(syncDone)
	}()

	select {
	case <-syncDone:
		if syncErr != nil {
			return syncErr
		}
		// Cache synced successfully
	case <-syncCtx.Done():
		// Timeout - cluster might be unreachable or API server is slow
		return fmt.Errorf("timeout waiting for RV informer cache sync: cluster may be unreachable")
	case <-c.informerStop:
		// Informer was stopped (shouldn't happen during init)
		return fmt.Errorf("informer stopped unexpectedly during initialization")
	}

	c.rvInformerMu.Lock()
	c.informerReady = true
	c.rvInformerMu.Unlock()

	return nil
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

// StopInformers stops all running informers.
// Called on application shutdown from main.go via defer.
func (c *Client) StopInformers() {
	c.rvInformerMu.Lock()
	defer c.rvInformerMu.Unlock()

	if c.informerReady {
		close(c.informerStop)
		c.informerReady = false
	}
}

// RegisterRVChecker registers a VolumeChecker to receive events for specific RV.
// Returns channel where RV updates will be sent. Caller must call UnregisterRVChecker on shutdown.
// Uses dispatcher pattern: one informer handler routes to many checkers via map lookup.
func (c *Client) RegisterRVChecker(rvName string, ch chan *v1alpha1.ReplicatedVolume) error {
	c.rvInformerMu.RLock()
	ready := c.informerReady
	c.rvInformerMu.RUnlock()

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
// Used by VolumeChecker.checkInitialState() to get RV without API call.
func (c *Client) GetRVFromCache(name string) (*v1alpha1.ReplicatedVolume, error) {
	c.rvInformerMu.RLock()
	defer c.rvInformerMu.RUnlock()

	if !c.informerReady {
		return nil, fmt.Errorf("RV informer is not ready")
	}

	obj, exists, err := c.rvInformer.GetStore().GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("RV %s not found in cache", name)
	}

	rv, ok := obj.(*v1alpha1.ReplicatedVolume)
	if !ok {
		return nil, fmt.Errorf("unexpected object type in cache: %T", obj)
	}

	return rv, nil
}

// Client returns the underlying controller-runtime client
func (c *Client) Client() client.Client {
	return c.cl
}

// GetRandomNodes selects n random unique nodes from the cluster
func (c *Client) GetRandomNodes(ctx context.Context, n int) ([]corev1.Node, error) {
	nodes, err := c.ListNodes(ctx)
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

// ListNodes returns all nodes in the cluster with label storage.deckhouse.io/sds-replicated-volume-node=""
// The result is cached with TTL of nodesCacheTTL
func (c *Client) ListNodes(ctx context.Context) ([]corev1.Node, error) {
	c.nodesMutex.RLock()
	if c.cachedNodes != nil && time.Since(c.nodesCacheTime) < nodesCacheTTL {
		nodes := make([]corev1.Node, len(c.cachedNodes))
		for i := range c.cachedNodes {
			nodes[i] = *c.cachedNodes[i].DeepCopy()
		}
		c.nodesMutex.RUnlock()
		return nodes, nil
	}
	c.nodesMutex.RUnlock()

	c.nodesMutex.Lock()
	defer c.nodesMutex.Unlock()

	// Double-check after acquiring write lock
	if c.cachedNodes != nil && time.Since(c.nodesCacheTime) < nodesCacheTTL {
		nodes := make([]corev1.Node, len(c.cachedNodes))
		for i := range c.cachedNodes {
			nodes[i] = *c.cachedNodes[i].DeepCopy()
		}
		return nodes, nil
	}

	nodeList := &corev1.NodeList{}
	err := c.cl.List(ctx, nodeList, client.MatchingLabels{
		"storage.deckhouse.io/sds-replicated-volume-node": "",
	})
	if err != nil {
		return nil, err
	}

	// Cache the result with timestamp
	c.cachedNodes = make([]corev1.Node, len(nodeList.Items))
	for i := range nodeList.Items {
		c.cachedNodes[i] = *nodeList.Items[i].DeepCopy()
	}
	c.nodesCacheTime = time.Now()

	// Return a deep copy to prevent external modifications
	nodes := make([]corev1.Node, len(c.cachedNodes))
	for i := range c.cachedNodes {
		nodes[i] = *c.cachedNodes[i].DeepCopy()
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

// GetRV gets a ReplicatedVolume by name (from API server, not cache)
func (c *Client) GetRV(ctx context.Context, name string) (*v1alpha1.ReplicatedVolume, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	err := c.cl.Get(ctx, client.ObjectKey{Name: name}, rv)
	if err != nil {
		return nil, err
	}
	return rv, nil
}

// IsRVReady checks if a ReplicatedVolume is in IOReady and Quorum conditions
func (c *Client) IsRVReady(rv *v1alpha1.ReplicatedVolume) bool {
	if rv.Status == nil {
		return false
	}
	return meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha1.ConditionTypeRVIOReady) &&
		meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha1.ConditionTypeRVQuorum)
}

// PatchRV patches a ReplicatedVolume using merge patch strategy
func (c *Client) PatchRV(ctx context.Context, originalRV *v1alpha1.ReplicatedVolume, updatedRV *v1alpha1.ReplicatedVolume) error {
	return c.cl.Patch(ctx, updatedRV, client.MergeFrom(originalRV))
}

// ListRVRsByRVName lists all ReplicatedVolumeReplicas for a given RV
// Filters by spec.replicatedVolumeName field
func (c *Client) ListRVRsByRVName(ctx context.Context, rvName string) ([]v1alpha1.ReplicatedVolumeReplica, error) {
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	err := c.cl.List(ctx, rvrList)
	if err != nil {
		return nil, err
	}

	// Filter by replicatedVolumeName
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
