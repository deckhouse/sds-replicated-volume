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

package chaos

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DVP VirtualMachine GVR
var vmGVR = schema.GroupVersionResource{
	Group:    "virtualization.deckhouse.io",
	Version:  "v1alpha2",
	Resource: "virtualmachines",
}

// ParentClient wraps a Kubernetes client for the DVP parent cluster
type ParentClient struct {
	cl          client.Client
	vmNamespace string
}

// NewParentClient creates a new client for the DVP parent cluster
func NewParentClient(kubeconfigPath, vmNamespace string) (*ParentClient, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("building parent config from kubeconfig file %s: %w", kubeconfigPath, err)
	}

	// Disable rate limiter for chaos operations
	cfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}

	return &ParentClient{
		cl:          cl,
		vmNamespace: vmNamespace,
	}, nil
}

// NewParentClientFromConfig creates a new client from existing rest.Config
func NewParentClientFromConfig(cfg *rest.Config, vmNamespace string) (*ParentClient, error) {
	// Disable rate limiter for chaos operations
	cfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("creating parent client: %w", err)
	}

	return &ParentClient{
		cl:          cl,
		vmNamespace: vmNamespace,
	}, nil
}

// ListVMs returns all VirtualMachines with their IPs
func (c *ParentClient) ListVMs(ctx context.Context) ([]NodeInfo, error) {
	vmList := &unstructured.UnstructuredList{}
	vmList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   vmGVR.Group,
		Version: vmGVR.Version,
		Kind:    "VirtualMachineList",
	})

	if err := c.cl.List(ctx, vmList, client.InNamespace(c.vmNamespace)); err != nil {
		return nil, fmt.Errorf("listing VMs in namespace %s: %w", c.vmNamespace, err)
	}

	var nodes []NodeInfo
	for _, vm := range vmList.Items {
		name := vm.GetName()

		// Get IP from status.ipAddress
		ipAddress, found, err := unstructured.NestedString(vm.Object, "status", "ipAddress")
		if err != nil || !found || ipAddress == "" {
			slog.Debug("skipping VM without IP address", "vm", name, "namespace", c.vmNamespace)
			continue
		}

		// Check phase is Running
		phase, _, _ := unstructured.NestedString(vm.Object, "status", "phase")
		if phase != "Running" {
			slog.Debug("skipping VM not in Running phase", "vm", name, "phase", phase)
			continue
		}

		nodes = append(nodes, NodeInfo{
			Name:      name,
			IPAddress: ipAddress,
		})
	}

	return nodes, nil
}

// Client returns the Kubernetes client for the parent cluster
func (c *ParentClient) Client() client.Client {
	return c.cl
}

// VMNamespace returns the namespace where VMs are located
func (c *ParentClient) VMNamespace() string {
	return c.vmNamespace
}
