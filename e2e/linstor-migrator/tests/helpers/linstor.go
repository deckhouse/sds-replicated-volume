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

package helpers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// linstorNode represents a Linstor node from the API.
type linstorNode struct {
	Name             string `json:"name"`
	ConnectionStatus string `json:"connection_status"`
}

// linstorStoragePool represents a Linstor storage pool from the API.
type linstorStoragePool struct {
	NodeName      string `json:"node_name"`
	StoragePool   string `json:"storage_pool_name"`
	Driver        string `json:"provider_kind"`
	FreeCapacity  int64  `json:"free_capacity"`
	TotalCapacity int64  `json:"total_capacity"`
}

// linstorResource represents a Linstor resource from the API.
type linstorResource struct {
	Name   string `json:"name"`
	Node   string `json:"node_name"`
	NodeID int    `json:"node_id"`
	Port   int    `json:"port"`
	Secret string `json:"secret"`
}

// linstorVolume represents a Linstor volume from the API.
type linstorVolume struct {
	Resource         string `json:"name"`
	StoragePool      string `json:"storage_pool_name"`
	Node             string `json:"node_name"`
	VolumeNumber     int    `json:"volume_number"`
	DevicePath       string `json:"device_path"`
	BackingDisk      string `json:"backing_disk"`
	DiskState        string `json:"disk_state"`
	InUse            bool   `json:"in_use"`
	AllocatedSizeKiB int64  `json:"allocated_size_kib"`
	MinorNumber      int    `json:"minor_number"`
}

// LinstorState holds the complete state of Linstor cluster.
type LinstorState struct {
	Timestamp    string               `json:"timestamp"`
	RunID        string               `json:"run_id"`
	Nodes        []linstorNode        `json:"nodes"`
	StoragePools []linstorStoragePool `json:"storage_pools"`
	Resources    []linstorResource    `json:"resources"`
	Volumes      []linstorVolume      `json:"volumes"`
}

// linstorExecResult holds the result of a Linstor command execution.
// Linstor with -m flag returns a slice of maps directly, not wrapped in a "data" field.
type linstorExecResult []map[string]interface{}

// execInLinstorController executes a command inside the linstor-controller pod.
func execInLinstorController(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config, cmd []string) (string, error) {
	podName, err := findLinstorControllerPod(ctx, clientset)
	if err != nil {
		return "", fmt.Errorf("failed to find linstor-controller pod: %w", err)
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespaceSRV).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: "linstor-controller",
		Command:   cmd,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("command failed: %w, stderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// findLinstorControllerPod finds a Running linstor-controller pod name.
// It filters out pods in Completed or other non-Running states.
func findLinstorControllerPod(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	pods, err := clientset.CoreV1().Pods(namespaceSRV).List(ctx, metav1.ListOptions{
		LabelSelector: "app=linstor-controller",
	})
	if err != nil {
		return "", err
	}

	// Find a Running pod (not Completed, CrashLoopBackOff, etc.)
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			return pod.Name, nil
		}
	}

	return "", fmt.Errorf("no Running linstor-controller pod found (found %d pods, none in Running state)", len(pods.Items))
}

// linstorNodeList retrieves the list of Linstor nodes using JSON output.
func linstorNodeList(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config) ([]linstorNode, error) {
	output, err := execInLinstorController(ctx, clientset, restConfig, []string{
		"originallinstor", "-m", "--output-version=v1", "node", "list",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute linstor node list: %w", err)
	}

	if IsTestDebugEnabled() {
		slog.Debug(fmt.Sprintf("Linstor node list JSON output: %s", output))
	}

	var result linstorExecResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		var nested [][]map[string]interface{}
		if errNested := json.Unmarshal([]byte(output), &nested); errNested != nil {
			return nil, fmt.Errorf("failed to parse linstor node list output: %w", err)
		}

		flattened := make(linstorExecResult, 0)
		for _, group := range nested {
			flattened = append(flattened, group...)
		}
		result = flattened
	}

	nodes := make([]linstorNode, 0, len(result))
	for _, item := range result {
		node := linstorNode{
			Name:             getString(item, "name"),
			ConnectionStatus: getString(item, "connection_status"),
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// linstorStoragePoolList retrieves the list of Linstor storage pools using JSON output.
func linstorStoragePoolList(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config) ([]linstorStoragePool, error) {
	output, err := execInLinstorController(ctx, clientset, restConfig, []string{
		"originallinstor", "-m", "--output-version=v1", "sp", "list",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute linstor sp list: %w", err)
	}

	if IsTestDebugEnabled() {
		slog.Debug(fmt.Sprintf("Linstor sp list JSON output: %s", output))
	}

	var result linstorExecResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		var nested [][]map[string]interface{}
		if errNested := json.Unmarshal([]byte(output), &nested); errNested != nil {
			return nil, fmt.Errorf("failed to parse linstor sp list output: %w", err)
		}

		flattened := make(linstorExecResult, 0)
		for _, group := range nested {
			flattened = append(flattened, group...)
		}
		result = flattened
	}

	pools := make([]linstorStoragePool, 0, len(result))
	for _, item := range result {
		pool := linstorStoragePool{
			NodeName:    getString(item, "node_name"),
			StoragePool: getString(item, "storage_pool_name"),
			Driver:      getString(item, "provider_kind"),
		}
		if fc, ok := item["free_capacity"].(float64); ok {
			pool.FreeCapacity = int64(fc)
		}
		if tc, ok := item["total_capacity"].(float64); ok {
			pool.TotalCapacity = int64(tc)
		}
		pools = append(pools, pool)
	}

	return pools, nil
}

// linstorResourceList retrieves the list of Linstor resources using JSON output.
func linstorResourceList(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config) ([]linstorResource, error) {
	output, err := execInLinstorController(ctx, clientset, restConfig, []string{
		"originallinstor", "-m", "--output-version=v1", "r", "list",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute linstor r list: %w", err)
	}

	if IsTestDebugEnabled() {
		slog.Debug(fmt.Sprintf("Linstor r list JSON output: %s", output))
	}

	var result linstorExecResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		var nested [][]map[string]interface{}
		if errNested := json.Unmarshal([]byte(output), &nested); errNested != nil {
			return nil, fmt.Errorf("failed to parse linstor r list output: %w", err)
		}

		flattened := make(linstorExecResult, 0)
		for _, group := range nested {
			flattened = append(flattened, group...)
		}
		result = flattened
	}

	resources := make([]linstorResource, 0, len(result))
	for _, item := range result {
		resource := linstorResource{
			Name:   getString(item, "name"),
			Node:   getString(item, "node_name"),
			NodeID: getIntPath(item, "layer_object", "drbd", "node_id"),
			Secret: getStringPath(item, "layer_object", "drbd", "drbd_resource_definition", "secret"),
		}
		resource.Port = getIntPath(item, "layer_object", "drbd", "drbd_resource_definition", "port")
		resources = append(resources, resource)
	}

	return resources, nil
}

// linstorVolumeList retrieves the list of Linstor volumes using JSON output.
func linstorVolumeList(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config) ([]linstorVolume, error) {
	output, err := execInLinstorController(ctx, clientset, restConfig, []string{
		"originallinstor", "-m", "--output-version=v1", "volume", "list",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute linstor volume list: %w", err)
	}

	if IsTestDebugEnabled() {
		slog.Debug(fmt.Sprintf("Linstor volume list JSON output: %s", output))
	}

	var result linstorExecResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		var nested [][]map[string]interface{}
		if errNested := json.Unmarshal([]byte(output), &nested); errNested != nil {
			return nil, fmt.Errorf("failed to parse linstor volume list output: %w", err)
		}

		flattened := make(linstorExecResult, 0)
		for _, group := range nested {
			flattened = append(flattened, group...)
		}
		result = flattened
	}

	volumes := make([]linstorVolume, 0)
	for _, item := range result {
		resourceName := getString(item, "name")
		nodeName := getString(item, "node_name")
		inUse := getBoolPath(item, "state", "in_use")

		volItems := getSlice(item, "volumes")
		if len(volItems) == 0 {
			continue
		}

		volItem, ok := volItems[0].(map[string]interface{})
		if !ok {
			continue
		}

		vol := linstorVolume{
			Resource:         resourceName,
			StoragePool:      getString(volItem, "storage_pool_name"),
			Node:             nodeName,
			VolumeNumber:     getInt(volItem, "volume_number"),
			DevicePath:       getString(volItem, "device_path"),
			DiskState:        getStringPath(volItem, "state", "disk_state"),
			InUse:            inUse,
			AllocatedSizeKiB: getInt64(volItem, "allocated_size_kib"),
		}

		layerData := getSlice(volItem, "layer_data_list")
		for _, layerRaw := range layerData {
			layer, ok := layerRaw.(map[string]interface{})
			if !ok {
				continue
			}
			layerType := getString(layer, "type")
			layerMap := getMap(layer, "data")
			if len(layerMap) == 0 {
				continue
			}

			if layerType == "DRBD" {
				if vol.BackingDisk == "" {
					vol.BackingDisk = getString(layerMap, "backing_device")
				}
				if vol.MinorNumber == 0 {
					vol.MinorNumber = getIntPath(layerMap, "drbd_volume_definition", "minor_number")
				}
				if vol.DevicePath == "" {
					vol.DevicePath = getString(layerMap, "device_path")
				}
			}

			if layerType == "STORAGE" {
				if vol.DevicePath == "" {
					vol.DevicePath = getString(layerMap, "device_path")
				}
				if vol.DiskState == "" {
					vol.DiskState = getString(layerMap, "disk_state")
				}
			}
		}

		volumes = append(volumes, vol)
	}

	return volumes, nil
}

func collectLinstorState(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config, runID string) (*LinstorState, error) {
	nodes, err := linstorNodeList(ctx, clientset, restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get linstor nodes: %w", err)
	}

	storagePools, err := linstorStoragePoolList(ctx, clientset, restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get linstor storage pools: %w", err)
	}

	resources, err := linstorResourceList(ctx, clientset, restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get linstor resources: %w", err)
	}

	volumes, err := linstorVolumeList(ctx, clientset, restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get linstor volumes: %w", err)
	}

	state := &LinstorState{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		RunID:        runID,
		Nodes:        nodes,
		StoragePools: storagePools,
		Resources:    resources,
		Volumes:      volumes,
	}

	return state, nil
}

// saveLinstorState retrieves the complete Linstor state and always stores it as JSON in /tmp.
func saveLinstorState(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config, runID string) (*LinstorState, error) {
	slog.Info(fmt.Sprintf("Saving Linstor state for runID %s", runID))

	state, err := collectLinstorState(ctx, clientset, restConfig, runID)
	if err != nil {
		return nil, err
	}

	stateJSON, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal linstor state: %w", err)
	}

	fileRunID := strings.ReplaceAll(runID, "/", "_")
	filePath := fmt.Sprintf("%s%s-%d.json", linstorStateFilePrefix, fileRunID, time.Now().UTC().Unix())
	if err := os.WriteFile(filePath, stateJSON, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write linstor state to %s: %w", filePath, err)
	}

	slog.Info(fmt.Sprintf("Linstor state saved to %s: %d nodes, %d storage pools, %d resources, %d volumes", filePath, len(state.Nodes), len(state.StoragePools), len(state.Resources), len(state.Volumes)))

	return state, nil
}

// logLinstorState prints a compact summary of the current Linstor state.
func logLinstorState(state *LinstorState) {
	if state == nil {
		slog.Warn("Linstor state is nil, nothing to log")
		return
	}

	slog.Info(fmt.Sprintf("Linstor state snapshot (runID: %s)", state.RunID))
	slog.Info(fmt.Sprintf("Nodes=%d StoragePools=%d Resources=%d Volumes=%d", len(state.Nodes), len(state.StoragePools), len(state.Resources), len(state.Volumes)))
}

// getString safely extracts a string value from a map.
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// getBool safely extracts a boolean value from a map.
func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// getInt safely extracts an int value from a map.
func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		}
	}
	return 0
}

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key].(map[string]interface{}); ok {
		return v
	}
	return nil
}

func getSlice(m map[string]interface{}, key string) []interface{} {
	if v, ok := m[key].([]interface{}); ok {
		return v
	}
	return nil
}

func getInt64(m map[string]interface{}, key string) int64 {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case float64:
			return int64(t)
		case int64:
			return t
		case int:
			return int64(t)
		}
	}
	return 0
}

func getIntPath(m map[string]interface{}, keys ...string) int {
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			return getInt(current, key)
		}
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return 0
		}
		current = next
	}
	return 0
}

func getStringPath(m map[string]interface{}, keys ...string) string {
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			return getString(current, key)
		}
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func getBoolPath(m map[string]interface{}, keys ...string) bool {
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			return getBool(current, key)
		}
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return false
		}
		current = next
	}
	return false
}
