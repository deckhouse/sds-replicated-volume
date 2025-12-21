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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

// Client wraps a controller-runtime client with helper methods
type Client struct {
	cl          client.Client
	scheme      *runtime.Scheme
	cachedNodes []corev1.Node
	nodesMutex  sync.RWMutex
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

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding corev1 to scheme: %w", err)
	}
	if err := v1alpha3.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding v1alpha3 to scheme: %w", err)
	}

	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}

	return &Client{cl: cl, scheme: scheme}, nil
}

// Client returns the underlying controller-runtime client
func (c *Client) Client() client.Client {
	return c.cl
}

// GetRandoxmNodes selects n random unique nodes from the cluster
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
// The result is cached for the lifetime of the Client instance
func (c *Client) ListNodes(ctx context.Context) ([]corev1.Node, error) {
	c.nodesMutex.RLock()
	if c.cachedNodes != nil {
		nodes := make([]corev1.Node, len(c.cachedNodes))
		copy(nodes, c.cachedNodes)
		c.nodesMutex.RUnlock()
		return nodes, nil
	}
	c.nodesMutex.RUnlock()

	c.nodesMutex.Lock()
	defer c.nodesMutex.Unlock()

	// Double-check after acquiring write lock
	if c.cachedNodes != nil {
		nodes := make([]corev1.Node, len(c.cachedNodes))
		copy(nodes, c.cachedNodes)
		return nodes, nil
	}

	nodeList := &corev1.NodeList{}
	err := c.cl.List(ctx, nodeList, client.MatchingLabels{
		"storage.deckhouse.io/sds-replicated-volume-node": "",
	})
	if err != nil {
		return nil, err
	}

	// Cache the result
	c.cachedNodes = make([]corev1.Node, len(nodeList.Items))
	copy(c.cachedNodes, nodeList.Items)

	return c.cachedNodes, nil
}

// CreateRV creates a new ReplicatedVolume
func (c *Client) CreateRV(ctx context.Context, rv *v1alpha3.ReplicatedVolume) error {
	return c.cl.Create(ctx, rv)
}

// DeleteRV creates a new ReplicatedVolume
func (c *Client) DeleteRV(ctx context.Context, rv *v1alpha3.ReplicatedVolume) error {
	return c.cl.Delete(ctx, rv)
}

// GetRV returns a ReplicatedVolume by name
func (c *Client) GetRV(ctx context.Context, name string) (*v1alpha3.ReplicatedVolume, error) {
	rv := &v1alpha3.ReplicatedVolume{}
	err := c.cl.Get(ctx, client.ObjectKey{Name: name}, rv)
	if err != nil {
		return nil, err
	}
	return rv, nil
}

// IsRVReady checks if a ReplicatedVolume is in IOReady condition
func (c *Client) IsRVReady(rv *v1alpha3.ReplicatedVolume) bool {
	if rv.Status == nil {
		return false
	}
	return meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha3.ConditionTypeIOReady)
}
