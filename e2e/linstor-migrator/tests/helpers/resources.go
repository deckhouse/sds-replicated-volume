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
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sncv1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvv1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// lvmVolumeGroupResult holds information about a created LVMVolumeGroup.
type lvmVolumeGroupResult struct {
	Name     string
	NodeName string
	VGName   string
	LVG      *sncv1.LVMVolumeGroup
}

// RSPResult holds information about a created ReplicatedStoragePool.
type RSPResult struct {
	Name     string
	Type     srvv1.ReplicatedStoragePoolType
	LVGNames []string
	RSP      *srvv1.ReplicatedStoragePool
}

// rscResult holds information about a created ReplicatedStorageClass.
type rscResult struct {
	Name        string
	Replication int
	StorageType srvv1.ReplicatedStoragePoolType
	RSPName     string
	RSC         *srvv1.ReplicatedStorageClass
}

// PodPVCResult holds information about a created Pod and PVC.
type PodPVCResult struct {
	PodName  string
	PVCName  string
	RSCName  string
	NodeName string
}

// createLVMVolumeGroups creates LVMVolumeGroups from the attached BlockDevices
// and waits until all created LVGs reach Ready phase.
func createLVMVolumeGroups(ctx context.Context, c client.Client, attachedDisks []*attachedDiskResult, runID string) ([]*lvmVolumeGroupResult, error) {
	slog.Debug(fmt.Sprintf("CHECK: Creating LVMVolumeGroups from %d BlockDevices", len(attachedDisks)))

	results := make([]*lvmVolumeGroupResult, 0, len(attachedDisks))

	for i, disk := range attachedDisks {
		// Create safe names from node name
		nodeNameSafe := sanitizeNodeName(disk.NodeName)
		lvgName := fmt.Sprintf("migrator-e2e-lvg-%s-%s", runID, nodeNameSafe)
		vgName := fmt.Sprintf("migrator-e2e-vg-%s", nodeNameSafe)
		thinPoolName := "thindata"

		slog.Info(fmt.Sprintf("Creating LVMVolumeGroup %s on node %s", lvgName, disk.NodeName))

		lvg := &sncv1.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name: lvgName,
				Labels: map[string]string{
					"migrator-e2e": runID,
				},
			},
			Spec: sncv1.LVMVolumeGroupSpec{
				ActualVGNameOnTheNode: vgName,
				BlockDeviceSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "kubernetes.io/metadata.name",
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{disk.BlockDevice.Name},
						},
					},
				},
				Type: "Local",
				Local: sncv1.LVMVolumeGroupLocalSpec{
					NodeName: disk.NodeName,
				},
				ThinPools: []sncv1.LVMVolumeGroupThinPoolSpec{
					{
						Name:            thinPoolName,
						Size:            "50%",
						AllocationLimit: "200%",
					},
				},
			},
		}

		if err := c.Create(ctx, lvg); err != nil {
			return nil, fmt.Errorf("failed to create LVMVolumeGroup %s: %w", lvgName, err)
		}

		results = append(results, &lvmVolumeGroupResult{
			Name:     lvgName,
			NodeName: disk.NodeName,
			VGName:   vgName,
			LVG:      lvg,
		})

		slog.Info(fmt.Sprintf("Created LVMVolumeGroup %s (%d/%d)", lvgName, i+1, len(attachedDisks)))
	}

	// Wait for all LVMVolumeGroups to become Ready
	slog.Info(fmt.Sprintf("Waiting for %d LVMVolumeGroups to become Ready", len(results)))

	deadline := time.Now().Add(lvmVolumeGroupWaitTimeout)
	for time.Now().Before(deadline) {
		allReady := true
		for _, result := range results {
			var lvg sncv1.LVMVolumeGroup
			if err := c.Get(ctx, client.ObjectKey{Name: result.Name}, &lvg); err != nil {
				allReady = false
				break
			}
			if lvg.Status.Phase != sncv1.PhaseReady {
				allReady = false
				break
			}
			result.LVG = &lvg // Update with latest status
		}
		if allReady {
			break
		}
		time.Sleep(pollingInterval)
	}

	// Verify all are ready
	for _, result := range results {
		var lvg sncv1.LVMVolumeGroup
		if err := c.Get(ctx, client.ObjectKey{Name: result.Name}, &lvg); err != nil {
			return nil, fmt.Errorf("failed to get LVMVolumeGroup %s: %w", result.Name, err)
		}
		if lvg.Status.Phase != sncv1.PhaseReady {
			return nil, fmt.Errorf("LVMVolumeGroup %s is not Ready (phase: %s)", result.Name, lvg.Status.Phase)
		}
	}

	slog.Debug(fmt.Sprintf("PASS: All %d LVMVolumeGroups are Ready", len(results)))
	return results, nil
}

// createReplicatedStoragePools creates thick and thin ReplicatedStoragePools
// from the LVMVolumeGroups and waits until all created RSPs reach Ready phase.
func createReplicatedStoragePools(ctx context.Context, c client.Client, lvgs []*lvmVolumeGroupResult, runID string) ([]*RSPResult, error) {
	slog.Debug(fmt.Sprintf("CHECK: Creating ReplicatedStoragePools from %d LVMVolumeGroups", len(lvgs)))

	if len(lvgs) == 0 {
		return nil, fmt.Errorf("no LVMVolumeGroups provided")
	}

	results := make([]*RSPResult, 0, 2)

	// Prepare LVMVolumeGroups for thin RSP (with thin pool)
	thinLVMGroups := make([]srvv1.ReplicatedStoragePoolLVMVolumeGroups, 0, len(lvgs))
	// Prepare LVMVolumeGroups for thick RSP (without thin pool)
	thickLVMGroups := make([]srvv1.ReplicatedStoragePoolLVMVolumeGroups, 0, len(lvgs))
	for _, lvg := range lvgs {
		thinLVMGroups = append(thinLVMGroups, srvv1.ReplicatedStoragePoolLVMVolumeGroups{
			Name:         lvg.Name,
			ThinPoolName: "thindata",
		})
		thickLVMGroups = append(thickLVMGroups, srvv1.ReplicatedStoragePoolLVMVolumeGroups{
			Name: lvg.Name,
		})
	}

	// Create thick RSP
	thickRSPName := fmt.Sprintf("migrator-e2e-thick-%s", runID)
	slog.Info(fmt.Sprintf("Creating thick ReplicatedStoragePool %s", thickRSPName))

	thickRSP := &srvv1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: thickRSPName,
			Labels: map[string]string{
				"migrator-e2e": runID,
			},
		},
		Spec: srvv1.ReplicatedStoragePoolSpec{
			Type:            srvv1.ReplicatedStoragePoolTypeLVM,
			LVMVolumeGroups: thickLVMGroups,
			EligibleNodesPolicy: srvv1.ReplicatedStoragePoolEligibleNodesPolicy{
				NotReadyGracePeriod: metav1.Duration{Duration: 10 * time.Minute},
			},
		},
	}

	if err := c.Create(ctx, thickRSP); err != nil {
		return nil, fmt.Errorf("failed to create thick RSP %s: %w", thickRSPName, err)
	}

	results = append(results, &RSPResult{
		Name:     thickRSPName,
		Type:     srvv1.ReplicatedStoragePoolTypeLVM,
		LVGNames: getLVGNames(lvgs),
		RSP:      thickRSP,
	})

	// Create thin RSP
	thinRSPName := fmt.Sprintf("migrator-e2e-thin-%s", runID)
	slog.Info(fmt.Sprintf("Creating thin ReplicatedStoragePool %s", thinRSPName))

	thinRSP := &srvv1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: thinRSPName,
			Labels: map[string]string{
				"migrator-e2e": runID,
			},
		},
		Spec: srvv1.ReplicatedStoragePoolSpec{
			Type:            srvv1.ReplicatedStoragePoolTypeLVMThin,
			LVMVolumeGroups: thinLVMGroups,
			EligibleNodesPolicy: srvv1.ReplicatedStoragePoolEligibleNodesPolicy{
				NotReadyGracePeriod: metav1.Duration{Duration: 10 * time.Minute},
			},
		},
	}

	if err := c.Create(ctx, thinRSP); err != nil {
		return nil, fmt.Errorf("failed to create thin RSP %s: %w", thinRSPName, err)
	}

	results = append(results, &RSPResult{
		Name:     thinRSPName,
		Type:     srvv1.ReplicatedStoragePoolTypeLVMThin,
		LVGNames: getLVGNames(lvgs),
		RSP:      thinRSP,
	})

	slog.Info(fmt.Sprintf("Waiting for %d ReplicatedStoragePools to become Ready", len(results)))

	deadline := time.Now().Add(replicatedStoragePoolWaitTimeout)
	for time.Now().Before(deadline) {
		allReady := true
		for _, result := range results {
			var rsp srvv1.ReplicatedStoragePool
			if err := c.Get(ctx, client.ObjectKey{Name: result.Name}, &rsp); err != nil {
				allReady = false
				break
			}
			if !isRSPReady(rsp.Status.Phase) {
				allReady = false
				break
			}
			result.RSP = &rsp // Update with latest status
		}
		if allReady {
			break
		}
		time.Sleep(pollingInterval)
	}

	// Verify all are ready
	for _, result := range results {
		var rsp srvv1.ReplicatedStoragePool
		if err := c.Get(ctx, client.ObjectKey{Name: result.Name}, &rsp); err != nil {
			return nil, fmt.Errorf("failed to get ReplicatedStoragePool %s: %w", result.Name, err)
		}
		if !isRSPReady(rsp.Status.Phase) {
			return nil, fmt.Errorf("ReplicatedStoragePool %s is not Ready (phase: %s)", result.Name, rsp.Status.Phase)
		}
	}

	slog.Debug(fmt.Sprintf("PASS: Created and verified 2 ReplicatedStoragePools (thick: %s, thin: %s)", thickRSPName, thinRSPName))
	return results, nil
}

func isRSPReady(phase srvv1.ReplicatedStoragePoolPhase) bool {
	return phase == srvv1.ReplicatedStoragePoolPhaseReady || string(phase) == "Completed"
}

// createReplicatedStorageClasses creates 6 ReplicatedStorageClasses: r1-thick, r2-thick, r3-thick, r1-thin, r2-thin, r3-thin.
// Parameters are fixed: topology Ignored, volumeAccess PreferablyLocal.
func createReplicatedStorageClasses(ctx context.Context, c client.Client, rspResults []*RSPResult, runID string) ([]*rscResult, error) {
	slog.Debug("CHECK: Creating ReplicatedStorageClasses")

	results := make([]*rscResult, 0, 6)

	// Find thick and thin RSPs
	var thickRSP, thinRSP *RSPResult
	for _, rsp := range rspResults {
		switch rsp.Type {
		case srvv1.ReplicatedStoragePoolTypeLVM:
			thickRSP = rsp
		case srvv1.ReplicatedStoragePoolTypeLVMThin:
			thinRSP = rsp
		}
	}

	if thickRSP == nil || thinRSP == nil {
		return nil, fmt.Errorf("missing thick or thin RSP: thick=%v, thin=%v", thickRSP != nil, thinRSP != nil)
	}

	// Create RSCs for each replication factor and storage type
	replications := []int{1, 2, 3}
	storageTypes := []struct {
		name  string
		rsp   *RSPResult
		type_ srvv1.ReplicatedStoragePoolType
	}{
		{"thick", thickRSP, srvv1.ReplicatedStoragePoolTypeLVM},
		{"thin", thinRSP, srvv1.ReplicatedStoragePoolTypeLVMThin},
	}

	for _, st := range storageTypes {
		for _, repl := range replications {
			rscName := fmt.Sprintf("migrator-e2e-r%d-%s-%s", repl, st.name, runID)

			slog.Info(fmt.Sprintf("Creating ReplicatedStorageClass %s (replication: %d, type: %s)", rscName, repl, st.name))

			topology := srvv1.TopologyIgnored
			volumeAccess := srvv1.VolumeAccessPreferablyLocal

			rsc := &srvv1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: rscName,
					Labels: map[string]string{
						"migrator-e2e": runID,
					},
				},
				Spec: srvv1.ReplicatedStorageClassSpec{
					// Use legacy RSC format because this phase runs before migration,
					// when old control plane and old CRD schema are active.
					StoragePool:   st.rsp.Name,
					Replication:   legacyReplicationByFactor(repl),
					Topology:      topology,
					VolumeAccess:  volumeAccess,
					ReclaimPolicy: srvv1.RSCReclaimPolicyDelete,
				},
			}

			if err := c.Create(ctx, rsc); err != nil {
				return nil, fmt.Errorf("failed to create RSC %s: %w", rscName, err)
			}

			results = append(results, &rscResult{
				Name:        rscName,
				Replication: repl,
				StorageType: st.type_,
				RSPName:     st.rsp.Name,
				RSC:         rsc,
			})
		}
	}

	slog.Debug(fmt.Sprintf("PASS: Created %d ReplicatedStorageClasses", len(results)))
	return results, nil
}

func legacyReplicationByFactor(replication int) srvv1.ReplicatedStorageClassReplication {
	switch replication {
	case 1:
		return srvv1.ReplicationNone
	case 2:
		return srvv1.ReplicationAvailability
	default:
		return srvv1.ReplicationConsistencyAndAvailability
	}
}

// createPodWithPVC creates a Pod with a PVC using the specified StorageClass.
// The Pod runs a simple busybox container that writes a test file.
func createPodWithPVC(ctx context.Context, c client.Client, rscName string, runID string, index int) (*PodPVCResult, error) {
	podName := fmt.Sprintf("migrator-e2e-pod-%s-%d", runID, index)
	pvcName := fmt.Sprintf("migrator-e2e-pvc-%s-%d", runID, index)

	slog.Info(fmt.Sprintf("Creating Pod %s with PVC %s (StorageClass: %s)", podName, pvcName, rscName))

	// Create PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: TestNamespace,
			Labels: map[string]string{
				"migrator-e2e": runID,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
			StorageClassName: &rscName,
		},
	}

	if err := c.Create(ctx, pvc); err != nil {
		return nil, fmt.Errorf("failed to create PVC %s: %w", pvcName, err)
	}

	// Create Pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: TestNamespace,
			Labels: map[string]string{
				"migrator-e2e": runID,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "test",
					Image:   "busybox:latest",
					Command: []string{"sh", "-ec", "while true; do sleep 3600; done"},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data",
							MountPath: "/data",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "data",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}

	if err := c.Create(ctx, pod); err != nil {
		return nil, fmt.Errorf("failed to create Pod %s: %w", podName, err)
	}

	return &PodPVCResult{
		PodName: podName,
		PVCName: pvcName,
		RSCName: rscName,
	}, nil
}

// createPodsWithPVCs creates one Pod+PVC per provided RSC and waits
// until all created Pods become Ready.
func createPodsWithPVCs(ctx context.Context, c client.Client, rscResults []*rscResult, runID string) ([]*PodPVCResult, error) {
	results := make([]*PodPVCResult, 0, len(rscResults))
	for i, rsc := range rscResults {
		result, err := createPodWithPVC(ctx, c, rsc.Name, runID, i)
		if err != nil {
			return nil, fmt.Errorf("failed to create Pod with PVC for RSC %s: %w", rsc.Name, err)
		}
		results = append(results, result)
	}

	podNames := make([]string, len(results))
	for i, p := range results {
		podNames[i] = p.PodName
	}
	if err := waitForPodsReady(ctx, c, podNames, podReadyWaitTimeout); err != nil {
		return nil, err
	}

	return results, nil
}

// waitForPodsReady waits for all specified pods to be in Ready state.
func waitForPodsReady(ctx context.Context, c client.Client, podNames []string, timeout time.Duration) error {
	slog.Info(fmt.Sprintf("Waiting for %d Pods to become Ready (timeout: %v)", len(podNames), timeout))

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allReady := true
		for _, podName := range podNames {
			var pod corev1.Pod
			if err := c.Get(ctx, client.ObjectKey{Name: podName, Namespace: TestNamespace}, &pod); err != nil {
				allReady = false
				break
			}
			if pod.Status.Phase != corev1.PodRunning {
				allReady = false
				break
			}
		}
		if allReady {
			slog.Debug(fmt.Sprintf("PASS: All %d Pods are Ready", len(podNames)))
			return nil
		}
		time.Sleep(pollingInterval)
	}

	return fmt.Errorf("timeout waiting for Pods to become Ready after %v", timeout)
}

// sanitizeNodeName converts a node name to a safe string for resource names.
func sanitizeNodeName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, name)
}

// getLVGNames extracts LVG names from LVMVolumeGroupResult slice.
func getLVGNames(lvgs []*lvmVolumeGroupResult) []string {
	names := make([]string, len(lvgs))
	for i, lvg := range lvgs {
		names[i] = lvg.Name
	}
	return names
}
