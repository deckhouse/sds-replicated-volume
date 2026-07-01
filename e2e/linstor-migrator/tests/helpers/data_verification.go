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
	"fmt"
	"log/slog"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fileChecksum holds the checksum information for a file in a pod.
type fileChecksum struct {
	PodName  string
	FilePath string
	Checksum string
}

// writeFileWithChecksum writes a test file of the specified size and calculates its SHA256 checksum.
// It executes commands inside the pod using kubectl exec.
func writeFileWithChecksum(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config, podName, namespace string) (*fileChecksum, error) {
	slog.Info(fmt.Sprintf("Writing test file to pod %s", podName))

	filePath := "/data/testfile.bin"
	checksumPath := "/data/testfile.sha256"

	// Write a file with random data using dd
	writeCmd := []string{
		"dd", "if=/dev/urandom", fmt.Sprintf("of=%s", filePath),
		"bs=1M", fmt.Sprintf("count=%d", 10), // 10 MiB file
	}

	_, err := execInPodWithOutput(ctx, clientset, restConfig, podName, namespace, writeCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to write file in pod %s: %w", podName, err)
	}

	// Calculate SHA256 checksum
	checksumCmd := []string{"sha256sum", filePath}
	output, err := execInPodWithOutput(ctx, clientset, restConfig, podName, namespace, checksumCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate checksum in pod %s: %w", podName, err)
	}

	// Parse checksum from output (format: "<checksum>  <filename>")
	checksum := extractChecksum(output)

	// Save checksum to file for later verification
	saveChecksumCmd := []string{"sh", "-c", fmt.Sprintf("echo %s > %s", checksum, checksumPath)}
	_, err = execInPodWithOutput(ctx, clientset, restConfig, podName, namespace, saveChecksumCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to save checksum in pod %s: %w", podName, err)
	}

	slog.Info(fmt.Sprintf("Written 10Mi file to pod %s, checksum: %s", podName, checksum))

	return &fileChecksum{
		PodName:  podName,
		FilePath: filePath,
		Checksum: checksum,
	}, nil
}

// verifyFileChecksum verifies that the current checksum of /data/testfile.bin matches the expected checksum.
func verifyFileChecksum(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config, podName, namespace, expectedChecksum string) error {
	output, err := execInPodWithOutput(ctx, clientset, restConfig, podName, namespace, []string{"sha256sum", "/data/testfile.bin"})
	if err != nil {
		return fmt.Errorf("failed to calculate current checksum in pod %s: %w", podName, err)
	}
	actualChecksum := extractChecksum(output)

	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch in pod %s: expected %s, got %s", podName, expectedChecksum, actualChecksum)
	}

	slog.Debug(fmt.Sprintf("PASS: Checksum verified in pod %s", podName))
	return nil
}

// VerifyAllChecksums verifies the checksums of all test pods.
// It collects all errors and returns them as a slice.
func VerifyAllChecksums(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config, podNames []string, expectedChecksums map[string]string) []error {
	slog.Debug(fmt.Sprintf("CHECK: Verifying checksums in %d pods", len(podNames)))

	var errors []error
	for _, podName := range podNames {
		expectedChecksum, ok := expectedChecksums[podName]
		if !ok {
			errors = append(errors, fmt.Errorf("no expected checksum for pod %s", podName))
			continue
		}

		if err := verifyFileChecksum(ctx, clientset, restConfig, podName, TestNamespace, expectedChecksum); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) == 0 {
		slog.Debug(fmt.Sprintf("PASS: All %d checksums verified", len(podNames)))
	}

	return errors
}

// VerifyNewControlPlaneRunning checks that the new control plane components are running.
func VerifyNewControlPlaneRunning(ctx context.Context, c client.Client) error {
	// Check controller deployment
	var controller appsv1.Deployment
	if err := c.Get(ctx, client.ObjectKey{
		Namespace: namespaceSRV,
		Name:      newControllerDeployment,
	}, &controller); err != nil {
		return fmt.Errorf("failed to get new controller deployment: %w", err)
	}

	if controller.Status.ReadyReplicas == 0 {
		return fmt.Errorf("new controller has 0 ready replicas")
	}

	// Check agent daemonset
	var agent appsv1.DaemonSet
	if err := c.Get(ctx, client.ObjectKey{
		Namespace: namespaceSRV,
		Name:      newAgentDaemonSet,
	}, &agent); err != nil {
		return fmt.Errorf("failed to get new agent daemonset: %w", err)
	}

	if agent.Status.NumberReady != agent.Status.DesiredNumberScheduled || agent.Status.DesiredNumberScheduled == 0 {
		return fmt.Errorf("new agent has %d/%d ready pods", agent.Status.NumberReady, agent.Status.DesiredNumberScheduled)
	}

	slog.Debug(fmt.Sprintf("PASS: New control plane is running (controller: %d replicas, agent: %d/%d pods)", controller.Status.ReadyReplicas, agent.Status.NumberReady, agent.Status.DesiredNumberScheduled))
	return nil
}

// VerifyOldControlPlaneStopped checks that the old control plane components are stopped.
func VerifyOldControlPlaneStopped(ctx context.Context, c client.Client) error {
	// Check linstor-controller deployment is removed
	var oldController appsv1.Deployment
	err := c.Get(ctx, client.ObjectKey{
		Namespace: namespaceSRV,
		Name:      linstorControllerDeployment,
	}, &oldController)
	if err == nil {
		return fmt.Errorf("old linstor-controller deployment still exists")
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check old controller deployment: %w", err)
	}

	// Check linstor-node daemonset is removed
	var oldNode appsv1.DaemonSet
	err = c.Get(ctx, client.ObjectKey{
		Namespace: namespaceSRV,
		Name:      linstorNodeDaemonSet,
	}, &oldNode)
	if err == nil {
		return fmt.Errorf("old linstor-node daemonset still exists")
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check old node daemonset: %w", err)
	}
	return nil
}

// VerifyCSINodeDaemonSetRunning checks that csi-node DaemonSet is present and ready.
func VerifyCSINodeDaemonSetRunning(ctx context.Context, c client.Client) error {
	slog.Debug(fmt.Sprintf("CHECK: Waiting for csi-node DaemonSet to become ready (timeout: %v)", MigrationWaitTimeout))

	deadline := time.Now().Add(MigrationWaitTimeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for csi-node daemonset to become ready within %v", MigrationWaitTimeout)
		}

		time.Sleep(pollingInterval)

		var csiNode appsv1.DaemonSet
		err := c.Get(ctx, client.ObjectKey{
			Namespace: namespaceSRV,
			Name:      "csi-node",
		}, &csiNode)
		if err != nil {
			if apierrors.IsNotFound(err) {
				slog.Info("csi-node daemonset not yet created, waiting...")
				continue
			}
			return fmt.Errorf("failed to get csi-node daemonset: %w", err)
		}

		if csiNode.Status.DesiredNumberScheduled == 0 || csiNode.Status.NumberReady != csiNode.Status.DesiredNumberScheduled {
			slog.Info(fmt.Sprintf("csi-node has %d/%d ready pods, waiting...", csiNode.Status.NumberReady, csiNode.Status.DesiredNumberScheduled))
			continue
		}

		slog.Debug(fmt.Sprintf("PASS: csi-node daemonset is ready (%d/%d pods)", csiNode.Status.NumberReady, csiNode.Status.DesiredNumberScheduled))
		return nil
	}
}

// extractChecksum extracts the checksum from sha256sum output.
// Input format: "<checksum>  <filename>"
func extractChecksum(output string) string {
	// Trim whitespace and extract first field
	fields := bytes.Fields([]byte(output))
	if len(fields) > 0 {
		return string(fields[0])
	}
	return ""
}

// execInPodWithOutput executes a command in a pod and returns the output.
// It uses the Kubernetes exec API via SPDY.
func execInPodWithOutput(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config, podName, namespace string, cmd []string) (string, error) {
	return execInPodWithOutputInContainer(ctx, clientset, restConfig, podName, namespace, "", cmd)
}

// execInPodWithOutputInContainer executes a command in a specific pod container and returns output.
func execInPodWithOutputInContainer(
	ctx context.Context,
	clientset kubernetes.Interface,
	restConfig *rest.Config,
	podName,
	namespace,
	container string,
	cmd []string,
) (string, error) {
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: container,
		Command:   cmd,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("failed to create SPDY executor for pod %s: %w", podName, err)
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("command failed in pod %s: %w, stderr: %s", podName, err, stderr.String())
	}

	return stdout.String(), nil
}
