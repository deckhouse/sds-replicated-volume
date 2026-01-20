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
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// netemNamespace is the namespace where netem Jobs are created
	netemNamespace = "d8-sds-replicated-volume"

	// netemImage is the image used for netem Jobs
	netemImage = "alpine:latest"

	// netemJobTTL is the TTL for completed netem Jobs
	netemJobTTL int32 = 60

	// netemAutoCleanupBuffer is added to incident duration for auto-cleanup timeout.
	// If megatest doesn't cleanup within this buffer, the job self-cleans.
	netemAutoCleanupBuffer = 120 // seconds

	// Unique tc handles for chaos rules - chosen to be unlikely to conflict
	// with other tc configurations. We use "31337" (leet speak) which is
	// extremely unlikely to be used by other systems.
	chaosRootHandle  = "31337"
	chaosNetemHandle = "31338"
)

// NetemManager manages tc netem Jobs for network degradation simulation
type NetemManager struct {
	cl client.Client
}

// NewNetemManager creates a new NetemManager
func NewNetemManager(cl client.Client) *NetemManager {
	return &NetemManager{cl: cl}
}

// NetworkDegradationConfig configures network degradation parameters
type NetworkDegradationConfig struct {
	DelayMs          int           // Delay in milliseconds
	DelayJitter      int           // Delay jitter in milliseconds
	LossPercent      float64       // Packet loss percentage
	RateMbit         int           // Bandwidth limit in mbit/s (0 = no limit)
	IncidentDuration time.Duration // How long the degradation should last (for auto-cleanup timeout)
}

// ApplyNetworkDegradation creates a privileged Job to apply tc netem rules
// Returns the Job name for later cleanup
func (m *NetemManager) ApplyNetworkDegradation(ctx context.Context, nodeName, targetIP string, cfg NetworkDegradationConfig) (string, error) {
	jobName := fmt.Sprintf("chaos-netem-%s-%d", nodeName, time.Now().Unix())

	script := m.buildNetemScript(targetIP, cfg)
	job := m.buildNetemJob(jobName, nodeName, targetIP, script)

	if err := m.cl.Create(ctx, job); err != nil {
		return "", fmt.Errorf("creating netem Job %s: %w", jobName, err)
	}

	return jobName, nil
}

// RemoveNetworkDegradation creates a Job to remove tc netem rules from a node
func (m *NetemManager) RemoveNetworkDegradation(ctx context.Context, nodeName string) error {
	jobName := fmt.Sprintf("chaos-netem-cleanup-%s-%d", nodeName, time.Now().Unix())

	script := m.buildCleanupScript()
	job := m.buildCleanupJob(jobName, nodeName, script)

	if err := m.cl.Create(ctx, job); err != nil {
		return fmt.Errorf("creating cleanup Job %s: %w", jobName, err)
	}

	return nil
}

// CleanupAllNetemJobs deletes all netem Jobs created by chaos and runs cleanup scripts
func (m *NetemManager) CleanupAllNetemJobs(ctx context.Context) error {
	jobList := &batchv1.JobList{}

	if err := m.cl.List(ctx, jobList, client.InNamespace(netemNamespace), client.MatchingLabels{
		LabelChaosType: string(ChaosTypeNetworkDegrade),
	}); err != nil {
		return fmt.Errorf("listing netem Jobs: %w", err)
	}

	// First, create cleanup jobs for each node that has active netem rules
	cleanedNodes := make(map[string]bool)
	for _, job := range jobList.Items {
		nodeName := job.Labels[LabelChaosNodeA]
		if nodeName == "" || cleanedNodes[nodeName] {
			continue
		}

		// Create cleanup job for this node
		cleanupJobName := fmt.Sprintf("chaos-netem-final-cleanup-%s-%d", nodeName, time.Now().Unix())
		cleanupScript := m.buildCleanupScript()
		cleanupJob := m.buildCleanupJob(cleanupJobName, nodeName, cleanupScript)

		if err := m.cl.Create(ctx, cleanupJob); err != nil {
			// Ignore errors, best effort cleanup
			continue
		}
		cleanedNodes[nodeName] = true
	}

	// Delete all chaos netem Jobs with propagation policy to also delete Pods
	propagation := metav1.DeletePropagationBackground
	for _, job := range jobList.Items {
		if err := m.cl.Delete(ctx, &job, &client.DeleteOptions{
			PropagationPolicy: &propagation,
		}); err != nil {
			// Ignore errors, best effort cleanup
			continue
		}
	}

	return nil
}

// CleanupStaleNetemJobs cleans up any leftover netem Jobs from previous runs
// Should be called at startup
func (m *NetemManager) CleanupStaleNetemJobs(ctx context.Context) (int, error) {
	return m.cleanupJobsByLabel(ctx)
}

func (m *NetemManager) cleanupJobsByLabel(ctx context.Context) (int, error) {
	jobList := &batchv1.JobList{}

	if err := m.cl.List(ctx, jobList, client.InNamespace(netemNamespace), client.MatchingLabels{
		LabelChaosType: string(ChaosTypeNetworkDegrade),
	}); err != nil {
		return 0, fmt.Errorf("listing stale netem Jobs: %w", err)
	}

	if len(jobList.Items) == 0 {
		return 0, nil
	}

	// First run cleanup on each node
	cleanedNodes := make(map[string]bool)
	for _, job := range jobList.Items {
		nodeName := job.Labels[LabelChaosNodeA]
		if nodeName == "" || cleanedNodes[nodeName] {
			continue
		}

		cleanupJobName := fmt.Sprintf("chaos-netem-stale-cleanup-%s-%d", nodeName, time.Now().Unix())
		cleanupScript := m.buildCleanupScript()
		cleanupJob := m.buildCleanupJob(cleanupJobName, nodeName, cleanupScript)

		if err := m.cl.Create(ctx, cleanupJob); err != nil {
			continue
		}
		cleanedNodes[nodeName] = true
	}

	// Delete stale Jobs
	propagation := metav1.DeletePropagationBackground
	deleted := 0
	for _, job := range jobList.Items {
		if err := m.cl.Delete(ctx, &job, &client.DeleteOptions{
			PropagationPolicy: &propagation,
		}); err == nil {
			deleted++
		}
	}

	return deleted, nil
}

// buildNetemScript builds the shell script to apply tc netem rules safely.
// Uses unique handles (31337/31338) for reliable identification during cleanup.
// The script includes auto-cleanup timeout: if megatest doesn't cleanup within
// incident duration + buffer, the job will self-cleanup to prevent orphaned rules.
func (m *NetemManager) buildNetemScript(targetIP string, cfg NetworkDegradationConfig) string {
	// Calculate auto-cleanup timeout: incident duration + safety buffer
	autoCleanupTimeout := int(cfg.IncidentDuration.Seconds()) + netemAutoCleanupBuffer

	// Build rate parameter string (empty if no rate limit)
	rateParam := ""
	rateLogMsg := "none"
	if cfg.RateMbit > 0 {
		rateParam = fmt.Sprintf("rate %dmbit", cfg.RateMbit)
		rateLogMsg = fmt.Sprintf("%dmbit", cfg.RateMbit)
	}

	return fmt.Sprintf(`#!/bin/sh
set -e

# Install iproute2 for tc and ip commands
apk add --no-cache iproute2 >/dev/null 2>&1 || true

TARGET_IP="%s"
CHAOS_ROOT_HANDLE="%s"
CHAOS_NETEM_HANDLE="%s"
AUTO_CLEANUP_TIMEOUT=%d
RATE_PARAM="%s"

# Find the interface used to reach target IP
IFACE=$(ip route get $TARGET_IP 2>/dev/null | grep -oP 'dev \K\S+' | head -1)
if [ -z "$IFACE" ]; then
    echo "ERROR: Could not determine interface for target $TARGET_IP"
    exit 1
fi

echo "Target interface: $IFACE for $TARGET_IP"
echo "Auto-cleanup timeout: ${AUTO_CLEANUP_TIMEOUT}s"

# Check if our chaos handle is already in use anywhere
EXISTING_CHAOS=$(tc qdisc show 2>/dev/null | grep "handle ${CHAOS_ROOT_HANDLE}:" || true)
if [ -n "$EXISTING_CHAOS" ]; then
    echo "WARNING: Chaos handle ${CHAOS_ROOT_HANDLE}: already in use"
    echo "$EXISTING_CHAOS"
    echo "Removing existing chaos rules first"
    # Find and remove existing chaos qdisc
    for iface in $(ip link show 2>/dev/null | grep -oP '^\d+: \K[^:@]+' | grep -v lo); do
        QDISC_OUTPUT=$(tc qdisc show dev $iface 2>/dev/null || true)
        if echo "$QDISC_OUTPUT" | grep -q "handle ${CHAOS_ROOT_HANDLE}:"; then
            tc qdisc del dev $iface root 2>/dev/null || true
        fi
    done
fi

# Check if root qdisc already exists on target interface (not default)
EXISTING=$(tc qdisc show dev $IFACE 2>/dev/null | head -1 || true)
if echo "$EXISTING" | grep -qvE "pfifo_fast|fq_codel|noqueue|mq|pfifo"; then
    echo "WARNING: Interface $IFACE has non-default qdisc: $EXISTING"
    echo "Skipping to avoid conflicts with existing tc configuration"
    # Wait for auto-cleanup timeout then exit (rules were not applied)
    sleep $AUTO_CLEANUP_TIMEOUT
    exit 0
fi

echo "Applying netem rules on interface $IFACE for target $TARGET_IP"
echo "Using handles: root=${CHAOS_ROOT_HANDLE}: netem=${CHAOS_NETEM_HANDLE}:"

# Add prio qdisc as root with unique chaos handle
tc qdisc add dev $IFACE root handle ${CHAOS_ROOT_HANDLE}: prio 2>/dev/null || {
    echo "Failed to add root qdisc, interface may have existing configuration"
    exit 1
}

# Add netem qdisc with delay, loss, and optional rate limit (child of our prio qdisc)
tc qdisc add dev $IFACE parent ${CHAOS_ROOT_HANDLE}:3 handle ${CHAOS_NETEM_HANDLE}: netem delay %dms %dms loss %.2f%% $RATE_PARAM

# Add filter to route traffic to target IP through netem
tc filter add dev $IFACE protocol ip parent ${CHAOS_ROOT_HANDLE}:0 prio 3 u32 match ip dst $TARGET_IP/32 flowid ${CHAOS_ROOT_HANDLE}:3

echo "SUCCESS: Network degradation applied on $IFACE"
echo "  delay=%dms jitter=%dms loss=%.2f%% rate=%s to $TARGET_IP"

# Wait for auto-cleanup timeout.
# Normal flow: megatest creates cleanup job before this timeout expires.
# Fallback: if megatest crashes/hangs, we self-cleanup after timeout.
echo "Waiting ${AUTO_CLEANUP_TIMEOUT}s for cleanup signal or timeout..."
sleep $AUTO_CLEANUP_TIMEOUT

# Auto-cleanup: we reached timeout, megatest didn't cleanup
echo "Auto-cleanup: timeout reached, removing tc rules"
tc qdisc del dev $IFACE root 2>/dev/null || true
echo "Auto-cleanup completed"
`, targetIP, chaosRootHandle, chaosNetemHandle, autoCleanupTimeout, rateParam, cfg.DelayMs, cfg.DelayJitter, cfg.LossPercent, cfg.DelayMs, cfg.DelayJitter, cfg.LossPercent, rateLogMsg)
}

// buildCleanupScript builds the shell script to remove tc netem rules safely.
// Uses structure-based detection: only removes qdisc if it has BOTH:
// 1. prio qdisc with handle 31337: (root)
// 2. netem qdisc with parent 31337: (child)
// This ensures we never accidentally remove someone else's tc rules.
func (m *NetemManager) buildCleanupScript() string {
	return fmt.Sprintf(`#!/bin/sh

# Install iproute2 for tc command
apk add --no-cache iproute2 >/dev/null 2>&1 || true

CHAOS_ROOT_HANDLE="%s"

echo "Cleaning up tc netem rules created by chaos"
echo "Looking for chaos structure: prio handle ${CHAOS_ROOT_HANDLE}: with netem child"

# Scan all interfaces for our chaos qdisc structure
for iface in $(ip link show 2>/dev/null | grep -oP '^\d+: \K[^:@]+' | grep -v lo); do
    QDISC_OUTPUT=$(tc qdisc show dev $iface 2>/dev/null || true)
    
    # Check for our EXACT structure:
    # 1. Root prio qdisc with handle 31337:
    # 2. Child netem qdisc with parent 31337:
    ROOT_PRIO=$(echo "$QDISC_OUTPUT" | grep "prio.*handle ${CHAOS_ROOT_HANDLE}:" || true)
    CHILD_NETEM=$(echo "$QDISC_OUTPUT" | grep "netem.*parent ${CHAOS_ROOT_HANDLE}:" || true)
    
    if [ -n "$ROOT_PRIO" ] && [ -n "$CHILD_NETEM" ]; then
        echo "CONFIRMED: Found chaos qdisc structure on $iface"
        echo "  Root: $ROOT_PRIO"
        echo "  Child: $CHILD_NETEM"
        echo "Removing chaos qdisc from $iface"
        tc qdisc del dev $iface root 2>/dev/null || true
    fi
done

echo "Cleanup complete"
`, chaosRootHandle)
}

// buildNetemJob builds the Kubernetes Job for netem apply operations
func (m *NetemManager) buildNetemJob(name, nodeName, targetIP, script string) *batchv1.Job {
	privileged := true
	hostNetwork := true
	ttl := netemJobTTL

	// Sanitize targetIP for label (replace dots with dashes, max 63 chars)
	sanitizedIP := strings.ReplaceAll(targetIP, ".", "-")
	if len(sanitizedIP) > 63 {
		sanitizedIP = sanitizedIP[:63]
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: netemNamespace,
			Labels: map[string]string{
				LabelChaosType:     string(ChaosTypeNetworkDegrade),
				LabelChaosNodeA:    nodeName,
				LabelChaosTargetIP: sanitizedIP,
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelChaosType:     string(ChaosTypeNetworkDegrade),
						LabelChaosNodeA:    nodeName,
						LabelChaosTargetIP: sanitizedIP,
					},
				},
				Spec: corev1.PodSpec{
					HostNetwork:   hostNetwork,
					HostPID:       true,
					NodeName:      nodeName,
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "netem",
							Image:   netemImage,
							Command: []string{"/bin/sh", "-c", script},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{"NET_ADMIN"},
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildCleanupJob builds the Kubernetes Job for netem cleanup operations
func (m *NetemManager) buildCleanupJob(name, nodeName, script string) *batchv1.Job {
	privileged := true
	hostNetwork := true
	ttl := netemJobTTL

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: netemNamespace,
			Labels: map[string]string{
				LabelChaosType:  string(ChaosTypeNetworkDegrade),
				LabelChaosNodeA: nodeName,
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelChaosType:  string(ChaosTypeNetworkDegrade),
						LabelChaosNodeA: nodeName,
					},
				},
				Spec: corev1.PodSpec{
					HostNetwork:   hostNetwork,
					HostPID:       true,
					NodeName:      nodeName,
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "netem-cleanup",
							Image:   netemImage,
							Command: []string{"/bin/sh", "-c", script},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{"NET_ADMIN"},
								},
							},
						},
					},
				},
			},
		},
	}
}
