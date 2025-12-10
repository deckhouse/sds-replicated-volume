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

package k8sclient

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

const (
	maxRetries    = 5
	retryInterval = time.Second
)

// Client wraps a controller-runtime client with helper methods
type Client struct {
	cl     client.Client
	scheme *runtime.Scheme
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

// GetRV returns a ReplicatedVolume by name
func (c *Client) GetRV(ctx context.Context, name string) (*v1alpha3.ReplicatedVolume, error) {
	rv := &v1alpha3.ReplicatedVolume{}
	err := c.cl.Get(ctx, client.ObjectKey{Name: name}, rv)
	if err != nil {
		return nil, err
	}
	return rv, nil
}

// CreateRV creates a new ReplicatedVolume
func (c *Client) CreateRV(ctx context.Context, rv *v1alpha3.ReplicatedVolume) error {
	return c.cl.Create(ctx, rv)
}

// UpdateRV updates an existing ReplicatedVolume
func (c *Client) UpdateRV(ctx context.Context, rv *v1alpha3.ReplicatedVolume) error {
	return c.cl.Update(ctx, rv)
}

// DeleteRV deletes a ReplicatedVolume
func (c *Client) DeleteRV(ctx context.Context, name string) error {
	rv := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	return c.cl.Delete(ctx, rv)
}

// ListRVRs lists ReplicatedVolumeReplicas for a given RV
func (c *Client) ListRVRs(ctx context.Context, rvName string) ([]v1alpha3.ReplicatedVolumeReplica, error) {
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	err := c.cl.List(ctx, rvrList, client.MatchingFields{"spec.replicatedVolumeName": rvName})
	if err != nil {
		return nil, err
	}
	return rvrList.Items, nil
}

// CreateRVR creates a new ReplicatedVolumeReplica
func (c *Client) CreateRVR(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica) error {
	return c.cl.Create(ctx, rvr)
}

// DeleteRVR deletes a ReplicatedVolumeReplica by name
func (c *Client) DeleteRVR(ctx context.Context, name string) error {
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	return c.cl.Delete(ctx, rvr)
}

// GetRVR returns a ReplicatedVolumeReplica by name
func (c *Client) GetRVR(ctx context.Context, name string) (*v1alpha3.ReplicatedVolumeReplica, error) {
	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	err := c.cl.Get(ctx, client.ObjectKey{Name: name}, rvr)
	if err != nil {
		return nil, err
	}
	return rvr, nil
}

// ListNodes returns all nodes in the cluster
func (c *Client) ListNodes(ctx context.Context) ([]corev1.Node, error) {
	nodeList := &corev1.NodeList{}
	err := c.cl.List(ctx, nodeList)
	if err != nil {
		return nil, err
	}
	return nodeList.Items, nil
}

// ListPods lists pods by namespace and label selector
func (c *Client) ListPods(ctx context.Context, namespace string, labelSelector labels.Selector) ([]corev1.Pod, error) {
	podList := &corev1.PodList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: labelSelector},
	}
	err := c.cl.List(ctx, podList, opts...)
	if err != nil {
		return nil, err
	}
	return podList.Items, nil
}

// DeletePod deletes a pod
func (c *Client) DeletePod(ctx context.Context, namespace, name string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	return c.cl.Delete(ctx, pod)
}

// IsRVReady checks if a ReplicatedVolume is in Ready condition
func (c *Client) IsRVReady(rv *v1alpha3.ReplicatedVolume) bool {
	if rv.Status == nil {
		return false
	}
	return meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha3.ConditionTypeReady)
}

// IsRVQuorum checks if all replicas have quorum (simplified check via Ready condition)
func (c *Client) IsRVQuorum(rv *v1alpha3.ReplicatedVolume) bool {
	// For simplicity, we consider quorum achieved if the volume is Ready
	return c.IsRVReady(rv)
}

// WaitForRVReady waits for a ReplicatedVolume to become Ready
func (c *Client) WaitForRVReady(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := c.GetRV(ctx, name)
		if err != nil {
			if kerrors.IsNotFound(err) {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return err
		}

		if c.IsRVReady(rv) {
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for RV %s to become ready", name)
}

// WaitForRVDeleted waits for a ReplicatedVolume to be deleted
func (c *Client) WaitForRVDeleted(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, err := c.GetRV(ctx, name)
		if kerrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}

		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for RV %s to be deleted", name)
}

// SetPublishOn sets the publishOn field on a ReplicatedVolume
func (c *Client) SetPublishOn(ctx context.Context, name string, nodes []string) error {
	for attempt := 0; attempt < maxRetries; attempt++ {
		rv, err := c.GetRV(ctx, name)
		if err != nil {
			return err
		}

		rv.Spec.PublishOn = nodes
		err = c.cl.Update(ctx, rv)
		if err == nil {
			return nil
		}
		if !kerrors.IsConflict(err) {
			return err
		}
		time.Sleep(retryInterval)
	}
	return fmt.Errorf("failed to update publishOn after %d retries", maxRetries)
}

// WaitForPublishProvided waits for nodes to appear in publishedOn
func (c *Client) WaitForPublishProvided(ctx context.Context, name string, expectedNodes []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := c.GetRV(ctx, name)
		if err != nil {
			return err
		}

		if rv.Status != nil {
			allPresent := true
			for _, node := range expectedNodes {
				if !slices.Contains(rv.Status.PublishedOn, node) {
					allPresent = false
					break
				}
			}
			if allPresent {
				return nil
			}
		}

		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for nodes %v to appear in publishedOn", expectedNodes)
}

// WaitForPublishRemoved waits for nodes to be removed from publishedOn
func (c *Client) WaitForPublishRemoved(ctx context.Context, name string, removedNodes []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := c.GetRV(ctx, name)
		if err != nil {
			if kerrors.IsNotFound(err) {
				return nil // RV deleted means publish is effectively removed
			}
			return err
		}

		if rv.Status == nil {
			return nil
		}

		anyPresent := false
		for _, node := range removedNodes {
			if slices.Contains(rv.Status.PublishedOn, node) {
				anyPresent = true
				break
			}
		}
		if !anyPresent {
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for nodes %v to be removed from publishedOn", removedNodes)
}

// ResizeRV updates the size of a ReplicatedVolume
func (c *Client) ResizeRV(ctx context.Context, name string, newSize resource.Quantity) error {
	for attempt := 0; attempt < maxRetries; attempt++ {
		rv, err := c.GetRV(ctx, name)
		if err != nil {
			return err
		}

		rv.Spec.Size = newSize
		err = c.cl.Update(ctx, rv)
		if err == nil {
			return nil
		}
		if !kerrors.IsConflict(err) {
			return err
		}
		time.Sleep(retryInterval)
	}
	return fmt.Errorf("failed to resize RV after %d retries", maxRetries)
}

// WaitForResize waits for the actualSize to match or exceed the requested size
func (c *Client) WaitForResize(ctx context.Context, name string, expectedSize resource.Quantity, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := c.GetRV(ctx, name)
		if err != nil {
			return err
		}

		if rv.Status != nil && rv.Status.ActualSize != nil && rv.Status.ActualSize.Cmp(expectedSize) >= 0 {
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for RV %s to resize to %s", name, expectedSize.String())
}

// SelectRandomNode selects a random node from the cluster
func (c *Client) SelectRandomNode(ctx context.Context) (*corev1.Node, error) {
	nodes, err := c.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes found in cluster")
	}
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	return &nodes[rand.Intn(len(nodes))], nil
}

// SelectRandomNodes selects n random unique nodes from the cluster
func (c *Client) SelectRandomNodes(ctx context.Context, n int) ([]corev1.Node, error) {
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

// ListRVRsForVolume lists all ReplicatedVolumeReplicas for a specific volume using fallback method
func (c *Client) ListRVRsForVolume(ctx context.Context, rvName string) ([]v1alpha3.ReplicatedVolumeReplica, error) {
	// First try using field selector
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	err := c.cl.List(ctx, rvrList)
	if err != nil {
		return nil, err
	}

	// Filter manually since field selectors may not be indexed
	var result []v1alpha3.ReplicatedVolumeReplica
	for i := range rvrList.Items {
		if rvrList.Items[i].Spec.ReplicatedVolumeName == rvName {
			result = append(result, rvrList.Items[i])
		}
	}
	return result, nil
}
