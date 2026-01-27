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
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// networkDegradeNamespace is the namespace where network degrade Jobs are created
	networkDegradeNamespace = "default"

	// networkDegradeImage is the image used for network degrade Jobs
	networkDegradeImage = "krpsh/iperf3:0.1.0"

	// networkDegradeAutoCleanupBuffer is added to incident duration for auto-cleanup timeout
	networkDegradeAutoCleanupBuffer = 4 * time.Second

	// networkDegradeActiveDeadlineBuffer is added to incident duration for active deadline timeout
	networkDegradeActiveDeadlineBuffer = 2 * time.Second
)

// NetworkDegradeManager manages Jobs for network degradation (iptables/iperf3)
type NetworkDegradeManager struct {
	cl client.Client
}

// NewNetworkDegradeManager creates a new NetworkDegradeManager
func NewNetworkDegradeManager(cl client.Client) *NetworkDegradeManager {
	return &NetworkDegradeManager{cl: cl}
}

// ApplyPacketLoss creates Jobs to apply packet loss using iptables
// Creates two Jobs for each node:
//   - Job 1: adds iptables rule with comment (job name)
//   - Job 2: waits incidentDuration, then removes rule by comment
func (m *NetworkDegradeManager) ApplyPacketLoss(ctx context.Context, nodeA, nodeB NodeInfo, lossPercent float64, incidentDuration time.Duration) ([]string, error) {
	var jobNames []string

	// Create Jobs for nodeA
	jobNameA1 := fmt.Sprintf("%s-add-iptables", nodeA.Name)
	jobNameA2 := fmt.Sprintf("%s-del-iptables", nodeA.Name)

	// Check and delete existing Jobs before creating new ones
	if err := m.deleteJobIfExists(ctx, jobNameA1); err != nil {
		return nil, fmt.Errorf("deleting existing job %s: %w", jobNameA1, err)
	}
	if err := m.deleteJobIfExists(ctx, jobNameA2); err != nil {
		return nil, fmt.Errorf("deleting existing job %s: %w", jobNameA2, err)
	}

	// Job 1 for nodeA: add iptables rule
	jobA1 := m.buildPacketLossAddJob(jobNameA1, nodeA.Name, nodeB.IPAddress, lossPercent, incidentDuration)
	if err := m.cl.Create(ctx, jobA1); err != nil {
		return nil, fmt.Errorf("creating packet loss add job %s: %w", jobNameA1, err)
	}
	jobNames = append(jobNames, jobNameA1)

	// Job 2 for nodeA: remove iptables rule after incident duration
	jobA2 := m.buildPacketLossRemoveJob(jobNameA2, nodeA.Name, jobNameA1, incidentDuration)
	if err := m.cl.Create(ctx, jobA2); err != nil {
		return nil, fmt.Errorf("creating packet loss remove job %s: %w", jobNameA2, err)
	}
	jobNames = append(jobNames, jobNameA2)

	// Create Jobs for nodeB
	jobNameB1 := fmt.Sprintf("%s-add-iptables", nodeB.Name)
	jobNameB2 := fmt.Sprintf("%s-del-iptables", nodeB.Name)

	// Check and delete existing Jobs before creating new ones
	if err := m.deleteJobIfExists(ctx, jobNameB1); err != nil {
		return nil, fmt.Errorf("deleting existing job %s: %w", jobNameB1, err)
	}
	if err := m.deleteJobIfExists(ctx, jobNameB2); err != nil {
		return nil, fmt.Errorf("deleting existing job %s: %w", jobNameB2, err)
	}

	// Job 1 for nodeB: add iptables rule
	jobB1 := m.buildPacketLossAddJob(jobNameB1, nodeB.Name, nodeA.IPAddress, lossPercent, incidentDuration)
	if err := m.cl.Create(ctx, jobB1); err != nil {
		return nil, fmt.Errorf("creating packet loss add job %s: %w", jobNameB1, err)
	}
	jobNames = append(jobNames, jobNameB1)

	// Job 2 for nodeB: remove iptables rule after incident duration
	jobB2 := m.buildPacketLossRemoveJob(jobNameB2, nodeB.Name, jobNameB1, incidentDuration)
	if err := m.cl.Create(ctx, jobB2); err != nil {
		return nil, fmt.Errorf("creating packet loss remove job %s: %w", jobNameB2, err)
	}
	jobNames = append(jobNames, jobNameB2)

	return jobNames, nil
}

// ApplyLatency creates Jobs to apply latency using iperf3
// Creates one Job on each node
func (m *NetworkDegradeManager) ApplyLatency(ctx context.Context, nodeA, nodeB NodeInfo, incidentDuration time.Duration) ([]string, error) {
	var jobNames []string

	// Create Job for nodeA
	jobNameA := fmt.Sprintf("%s-latency", nodeA.Name)
	if err := m.deleteJobIfExists(ctx, jobNameA); err != nil {
		return nil, fmt.Errorf("deleting existing job %s: %w", jobNameA, err)
	}

	jobA := m.buildLatencyJob(jobNameA, nodeA.Name, nodeB.IPAddress, incidentDuration)
	if err := m.cl.Create(ctx, jobA); err != nil {
		return nil, fmt.Errorf("creating latency job %s: %w", jobNameA, err)
	}
	jobNames = append(jobNames, jobNameA)

	// Create Job for nodeB
	jobNameB := fmt.Sprintf("%s-latency", nodeB.Name)
	if err := m.deleteJobIfExists(ctx, jobNameB); err != nil {
		return nil, fmt.Errorf("deleting existing job %s: %w", jobNameB, err)
	}

	jobB := m.buildLatencyJob(jobNameB, nodeB.Name, nodeA.IPAddress, incidentDuration)
	if err := m.cl.Create(ctx, jobB); err != nil {
		return nil, fmt.Errorf("creating latency job %s: %w", jobNameB, err)
	}
	jobNames = append(jobNames, jobNameB)

	return jobNames, nil
}

// RemoveNetworkDegradation deletes Jobs by their names
func (m *NetworkDegradeManager) RemoveNetworkDegradation(ctx context.Context, jobNames []string) error {
	propagation := metav1.DeletePropagationBackground
	for _, jobName := range jobNames {
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName,
				Namespace: networkDegradeNamespace,
			},
		}
		if err := m.cl.Delete(ctx, job, &client.DeleteOptions{
			PropagationPolicy: &propagation,
		}); err != nil {
			// Ignore NotFound errors
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("deleting job %s: %w", jobName, err)
			}
		}
	}
	return nil
}

// CleanupStaleNetworkDegradeJobs cleans up any leftover Jobs from previous runs
// Should be called at startup. Returns number of deleted jobs.
func (m *NetworkDegradeManager) CleanupStaleNetworkDegradeJobs(ctx context.Context) (int, error) {
	jobList := &batchv1.JobList{}

	if err := m.cl.List(ctx, jobList, client.InNamespace(networkDegradeNamespace), client.MatchingLabels{
		LabelChaosType: string(ChaosTypeNetworkDegrade),
	}); err != nil {
		return 0, fmt.Errorf("listing stale network degrade Jobs: %w", err)
	}

	if len(jobList.Items) == 0 {
		return 0, nil
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
		// Ignore errors, best effort cleanup
	}

	return deleted, nil
}

// deleteJobIfExists deletes a Job if it exists
func (m *NetworkDegradeManager) deleteJobIfExists(ctx context.Context, jobName string) error {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: networkDegradeNamespace,
		},
	}
	if err := m.cl.Get(ctx, client.ObjectKey{Name: jobName, Namespace: networkDegradeNamespace}, job); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil // Job doesn't exist, nothing to delete
		}
		return err
	}

	propagation := metav1.DeletePropagationBackground
	return client.IgnoreNotFound(m.cl.Delete(ctx, job, &client.DeleteOptions{
		PropagationPolicy: &propagation,
	}))
}

// buildPacketLossAddJob builds a Job that adds iptables rule for packet loss
func (m *NetworkDegradeManager) buildPacketLossAddJob(jobName, nodeName, targetIP string, lossPercent float64, incidentDuration time.Duration) *batchv1.Job {
	privileged := true
	hostNetwork := true
	ttl := int32(int((incidentDuration + networkDegradeAutoCleanupBuffer).Seconds()))
	activeDeadline := int64((incidentDuration + networkDegradeActiveDeadlineBuffer).Seconds())

	script := fmt.Sprintf(`set -e
iptables -A INPUT -s %s -m statistic --mode random --probability %.2f -j DROP -m comment --comment "%s"
echo "iptables rule added"
`, targetIP, lossPercent, jobName)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: networkDegradeNamespace,
			Labels: map[string]string{
				LabelChaosType:  string(ChaosTypeNetworkDegrade),
				LabelChaosNodeA: nodeName,
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &activeDeadline,
			BackoffLimit:            int32Ptr(0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelChaosType:  string(ChaosTypeNetworkDegrade),
						LabelChaosNodeA: nodeName,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:                 corev1.RestartPolicyNever,
					HostNetwork:                   hostNetwork,
					NodeName:                      nodeName,
					TerminationGracePeriodSeconds: int64Ptr(1),
					Containers: []corev1.Container{
						{
							Name:    "net-tools",
							Image:   networkDegradeImage,
							Command: []string{"/bin/sh", "-c", script},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
						},
					},
				},
			},
		},
	}
}

// buildPacketLossRemoveJob builds a Job that removes iptables rule after incident duration
func (m *NetworkDegradeManager) buildPacketLossRemoveJob(jobName, nodeName, comment string, incidentDuration time.Duration) *batchv1.Job {
	privileged := true
	hostNetwork := true
	ttl := int32(int((incidentDuration + networkDegradeAutoCleanupBuffer).Seconds()))
	activeDeadline := int64((incidentDuration + networkDegradeActiveDeadlineBuffer).Seconds())

	script := fmt.Sprintf(`set -e
sleep %d
COMMENT="%s"
while iptables -L INPUT --line-numbers | grep -q "$COMMENT"; do
  NUMBER=$(iptables -L INPUT --line-numbers | grep -F "$COMMENT" | head -n1 | awk '{print $1}')
  echo "delete rule number $NUMBER"
  iptables -D INPUT $NUMBER
done
`, int(incidentDuration.Seconds()), comment)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: networkDegradeNamespace,
			Labels: map[string]string{
				LabelChaosType:  string(ChaosTypeNetworkDegrade),
				LabelChaosNodeA: nodeName,
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &activeDeadline,
			BackoffLimit:            int32Ptr(0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelChaosType:  string(ChaosTypeNetworkDegrade),
						LabelChaosNodeA: nodeName,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:                 corev1.RestartPolicyNever,
					HostNetwork:                   hostNetwork,
					NodeName:                      nodeName,
					TerminationGracePeriodSeconds: int64Ptr(1),
					Containers: []corev1.Container{
						{
							Name:    "net-tools",
							Image:   networkDegradeImage,
							Command: []string{"/bin/sh", "-c", script},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
						},
					},
				},
			},
		},
	}
}

// buildLatencyJob builds a Job that applies latency using iperf3
func (m *NetworkDegradeManager) buildLatencyJob(jobName, nodeName, targetIP string, incidentDuration time.Duration) *batchv1.Job {
	privileged := true
	hostNetwork := true
	ttl := int32(int((incidentDuration + networkDegradeAutoCleanupBuffer).Seconds()))
	activeDeadline := int64((incidentDuration + networkDegradeActiveDeadlineBuffer).Seconds())

	script := fmt.Sprintf(`set -e
timeout %d sh -c '
  iperf3 -s -D || true
  while true; do
    iperf3 -c %s -t 60 -i 30 || true
    sleep 0.5
  done
' || true
`, int(incidentDuration.Seconds()), targetIP)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: networkDegradeNamespace,
			Labels: map[string]string{
				LabelChaosType:  string(ChaosTypeNetworkDegrade),
				LabelChaosNodeA: nodeName,
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &activeDeadline,
			BackoffLimit:            int32Ptr(0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelChaosType:  string(ChaosTypeNetworkDegrade),
						LabelChaosNodeA: nodeName,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:                 corev1.RestartPolicyNever,
					HostNetwork:                   hostNetwork,
					NodeName:                      nodeName,
					TerminationGracePeriodSeconds: int64Ptr(1),
					Containers: []corev1.Container{
						{
							Name:    "net-tools",
							Image:   networkDegradeImage,
							Command: []string{"/bin/sh", "-c", script},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
						},
					},
				},
			},
		},
	}
}

// Helper functions for pointer conversion
func int32Ptr(i int32) *int32 {
	return &i
}

func int64Ptr(i int64) *int64 {
	return &i
}
