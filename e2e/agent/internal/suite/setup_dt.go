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

package suite

import (
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// BackingDevicePath returns the block device path for an LLV on the given node.
func BackingDevicePath(cluster *Cluster, nodeIdx int, llvName string) string {
	return "/dev/" + cluster.Nodes[nodeIdx].LVG.Spec.ActualVGNameOnTheNode + "/" + llvName
}

func drbdmetaReadDevUUIDArgs(backingDev string) []string {
	return []string{"drbdmeta", "-f", "0", "v09", backingDev, "internal", "read-dev-uuid"}
}

func drbdmetaWriteDevUUIDArgs(backingDev, uuid string) []string {
	return []string{"drbdmeta", "-f", "0", "v09", backingDev, "internal", "write-dev-uuid", uuid}
}

func readDevUUIDFromDisk(e envtesting.E, nodeExec *kubetesting.NodeExec, nodeName, backingDev string) string {
	return strings.TrimSpace(nodeExec.Exec(e, nodeName, drbdmetaReadDevUUIDArgs(backingDev)...))
}

func writeDevUUIDToDisk(e envtesting.E, nodeExec *kubetesting.NodeExec, nodeName, backingDev, uuid string) {
	nodeExec.Exec(e, nodeName, drbdmetaWriteDevUUIDArgs(backingDev, uuid)...)
}

func assertDeviceUUIDNonEmpty(e envtesting.E, drbdr *v1alpha1.DRBDResource) {
	e.Helper()
	if drbdr.Status.DeviceUUID == "" {
		e.Fatalf("assert: DRBDResource %q status.deviceUUID is empty", drbdr.Name)
	}
}

func assertDeviceUUIDEquals(e envtesting.E, drbdr *v1alpha1.DRBDResource, expected string) {
	e.Helper()
	if drbdr.Status.DeviceUUID != expected {
		e.Fatalf("assert: DRBDResource %q status.deviceUUID is %q, want %q",
			drbdr.Name, drbdr.Status.DeviceUUID, expected)
	}
}

func assertDeviceUUIDNotEquals(e envtesting.E, drbdr *v1alpha1.DRBDResource, notExpected string) {
	e.Helper()
	if drbdr.Status.DeviceUUID == notExpected {
		e.Fatalf("assert: DRBDResource %q status.deviceUUID is %q, expected different", drbdr.Name, drbdr.Status.DeviceUUID)
	}
}

func assertDeviceUUIDMatchesDisk(e envtesting.E, drbdr *v1alpha1.DRBDResource, diskUUID string) {
	e.Helper()
	if drbdr.Status.DeviceUUID == "" {
		e.Fatalf("assert: status deviceUUID is empty")
	}
	if diskUUID == "" {
		e.Fatalf("assert: disk deviceUUID is empty")
	}
	if drbdr.Status.DeviceUUID != diskUUID {
		e.Fatalf("assert: status deviceUUID %q does not match disk %q", drbdr.Status.DeviceUUID, diskUUID)
	}
}

func forceDeleteLLV(e envtesting.E, cl client.Client, name string, timeout time.Duration) {
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, llv); err != nil {
		return
	}
	if obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
		base := llv.DeepCopy()
		obju.RemoveFinalizer(llv, v1alpha1.AgentFinalizer)
		if err := cl.Patch(e.Context(), llv, client.MergeFrom(base)); err != nil {
			e.Fatalf("removing agent finalizer from LLV %q: %v", name, err)
		}
	}
	if llv.DeletionTimestamp == nil {
		if err := cl.Delete(e.Context(), llv); err != nil {
			e.Fatalf("deleting LLV %q: %v", name, err)
		}
	}
	waitForDeletion(e, cl, llv, timeout)
}

func waitForDiskUpToDate(e envtesting.E, cl client.WithWatch, drbdr *v1alpha1.DRBDResource) *v1alpha1.DRBDResource {
	if isDiskUpToDate(drbdr) {
		return drbdr
	}
	var diskTimeout DiskUpToDateTimeout
	e.Options(&diskTimeout)
	diskScope := e.ScopeWithTimeout(diskTimeout.Duration)
	defer diskScope.Close()
	watcherScope := e.Scope()
	defer watcherScope.Close()
	return kubetesting.SetupResourceWatcher(watcherScope, cl, drbdr)(diskScope, drbdr, isDiskUpToDate)
}

// SetupDT runs all device-uuid decision-table scenarios on one cluster.
func SetupDT(
	e envtesting.E,
	cl client.WithWatch,
	cluster *Cluster,
	nodeExec *kubetesting.NodeExec,
	logWatcher kubetesting.PodLogWatcher,
) {
	var testID TestID
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	var llvCreatedTimeout LLVCreatedTimeout
	e.Options(&testID, &drbdrConfiguredTimeout, &llvCreatedTimeout)
	node := cluster.Nodes[0]
	size := cluster.AllocateSize
	timeout := drbdrConfiguredTimeout.Duration

	e.Run("InitialCreation", func(e envtesting.E) {
		drbdr, _ := SetupDisklessToDiskfulReplica(e, cl, cluster, "dt7", 0)
		assertDeviceUUIDNonEmpty(e, drbdr)
		backingDev := BackingDevicePath(cluster, 0, drbdr.Name)
		diskUUID := readDevUUIDFromDisk(e, nodeExec, node.Name, backingDev)
		if diskUUID == "" || diskUUID == "0000000000000000" {
			e.Fatalf("assert: disk device-uuid is zero/empty after initial creation on %s", backingDev)
		}
		assertDeviceUUIDMatchesDisk(e, drbdr, diskUUID)
	})

	if len(cluster.Nodes) >= 2 {
		e.Run("Peered", func(e envtesting.E) {
			drbdrs := make([]*v1alpha1.DRBDResource, 2)
			for i := range drbdrs {
				drbdrs[i], _ = SetupDisklessToDiskfulReplica(e, cl, cluster, "dtp", i)
			}
			drbdrs = SetupPeering(e, cl, drbdrs)
			SetupInitialSync(e, cl, drbdrs)
			drbdr := drbdrs[0]

			e.Run("ReattachMatchingUUID", func(e envtesting.E) {
				assertDeviceUUIDNonEmpty(e, drbdr)
				saved := drbdr.Status.DeviceUUID
				drbdr = SetupStateDown(e, cl, drbdr)
				drbdr = SetupStateUp(e, cl, drbdr)
				assertDRBDRConfigured(e, drbdr)
				drbdr = waitForDiskUpToDate(e, cl, drbdr)
				assertDeviceUUIDEquals(e, drbdr, saved)
			})

			e.Run("ZeroDiskUUID", func(e envtesting.E) {
				assertDeviceUUIDNonEmpty(e, drbdr)
				saved := drbdr.Status.DeviceUUID
				backingDev := BackingDevicePath(cluster, 0, drbdr.Name)
				SetupStateDown(e, cl, drbdr)
				writeDevUUIDToDisk(e, nodeExec, node.Name, backingDev, "0000000000000000")
				drbdr = SetupStateUp(e, cl, drbdr)
				assertDRBDRConfigured(e, drbdr)
				drbdr = waitForDiskUpToDate(e, cl, drbdr)
				assertDeviceUUIDEquals(e, drbdr, saved)
				assertDeviceUUIDMatchesDisk(e, drbdr, readDevUUIDFromDisk(e, nodeExec, node.Name, backingDev))
			})

			e.Run("ForeignDisk", func(e envtesting.E) {
				logWatcher.SetupAllowErrors(e, "ForeignDiskDetected", "DEADBEEFDEADBEEF")
				errorCh := logWatcher.SetupLogLineWaiter(e, "DEADBEEFDEADBEEF")

				assertDeviceUUIDNonEmpty(e, drbdr)
				realUUID := drbdr.Status.DeviceUUID
				backingDev := BackingDevicePath(cluster, 0, drbdr.Name)
				SetupStateDown(e, cl, drbdr)
				writeDevUUIDToDisk(e, nodeExec, node.Name, backingDev, "0xdeadbeefdeadbeef")
				e.Cleanup(func() {
					writeDevUUIDToDisk(e, nodeExec, node.Name, backingDev, realUUID)
				})
				drbdr = SetupStateUp(e, cl, drbdr)
				assertDRBDRCondition(e, drbdr,
					v1alpha1.DRBDResourceCondConfiguredType,
					metav1.ConditionFalse,
					v1alpha1.DRBDResourceCondConfiguredReasonForeignDiskDetected,
				)

				select {
				case <-errorCh:
				case <-e.Context().Done():
					e.Fatalf("timed out waiting for ForeignDiskDetected error log")
				}

				writeDevUUIDToDisk(e, nodeExec, node.Name, backingDev, realUUID)
				drbdr = SetupStateDown(e, cl, drbdr)
				drbdr = SetupStateUp(e, cl, drbdr)
				assertDRBDRConfigured(e, drbdr)
				drbdr = waitForDiskUpToDate(e, cl, drbdr)
				assertDeviceUUIDEquals(e, drbdr, realUUID)
			})
		})
	}

	e.Run("AdoptFromDisk", func(e envtesting.E) {
		drbdr, _ := SetupDisklessToDiskfulReplica(e, cl, cluster, "dt3", 0)
		assertDeviceUUIDNonEmpty(e, drbdr)
		originalUUID := drbdr.Status.DeviceUUID
		drbdr = SetupStateDown(e, cl, drbdr)
		if err := cl.Delete(e.Context(), drbdr); err != nil {
			e.Fatalf("deleting DRBDResource %q: %v", drbdr.Name, err)
		}
		waitForDeletion(e, cl, drbdr, timeout)
		name2 := testID.ResourceName("dt3a", "0")
		drbdr2 := kubetesting.SetupResource(
			e.ScopeWithTimeout(timeout),
			cl,
			newDRBDResourceDiskless(name2, node.Name, 0),
			isDRBDRTerminal,
		)
		assertDRBDRConfigured(e, drbdr2)
		drbdr2 = kubetesting.SetupResourcePatch(
			e.ScopeWithTimeout(timeout),
			cl,
			client.ObjectKey{Name: name2},
			changeDRBDResourceToDiskful(drbdr.Name, size),
			isDRBDRTerminal,
		)
		assertDRBDRConfigured(e, drbdr2)
		assertDeviceUUIDEquals(e, drbdr2, originalUUID)
	})

	e.Run("GenerateNewUUID", func(e envtesting.E) {
		drbdr, _ := SetupDisklessToDiskfulReplica(e, cl, cluster, "dt5", 0)
		assertDeviceUUIDNonEmpty(e, drbdr)
		originalUUID := drbdr.Status.DeviceUUID
		backingDev := BackingDevicePath(cluster, 0, drbdr.Name)
		drbdr = SetupStateDown(e, cl, drbdr)
		writeDevUUIDToDisk(e, nodeExec, node.Name, backingDev, "0000000000000000")
		if err := cl.Delete(e.Context(), drbdr); err != nil {
			e.Fatalf("deleting DRBDResource %q: %v", drbdr.Name, err)
		}
		waitForDeletion(e, cl, drbdr, timeout)
		name2 := testID.ResourceName("dt5a", "0")
		drbdr2 := kubetesting.SetupResource(
			e.ScopeWithTimeout(timeout),
			cl,
			newDRBDResourceDiskless(name2, node.Name, 0),
			isDRBDRTerminal,
		)
		assertDRBDRConfigured(e, drbdr2)
		drbdr2 = kubetesting.SetupResourcePatch(
			e.ScopeWithTimeout(timeout),
			cl,
			client.ObjectKey{Name: name2},
			changeDRBDResourceToDiskful(drbdr.Name, size),
			isDRBDRTerminal,
		)
		assertDRBDRConfigured(e, drbdr2)
		assertDeviceUUIDNonEmpty(e, drbdr2)
		assertDeviceUUIDNotEquals(e, drbdr2, originalUUID)
	})

	e.Run("RecreateMetadata", func(e envtesting.E) {
		drbdr, _ := SetupDisklessToDiskfulReplica(e, cl, cluster, "dt6", 0)
		assertDeviceUUIDNonEmpty(e, drbdr)
		savedUUID := drbdr.Status.DeviceUUID
		drbdr = SetupStateDown(e, cl, drbdr)
		llvEmpty := testID.ResourceName("dt6e", "0")
		forceDeleteLLV(e, cl, llvEmpty, timeout)
		kubetesting.SetupResource(
			e.ScopeWithTimeout(llvCreatedTimeout.Duration),
			cl,
			newLLV(llvEmpty, size, node.LVG.Name),
			isLLVCreated,
		)
		backingDev := BackingDevicePath(cluster, 0, llvEmpty)
		nodeExec.Exec(e, node.Name, "drbdmeta", "0", "v09", backingDev, "internal", "create-md", "--force", "7")
		drbdr = kubetesting.SetupResourcePatch(
			e.ScopeWithTimeout(timeout),
			cl,
			client.ObjectKey{Name: drbdr.Name},
			changeDRBDResourceToDiskful(llvEmpty, size),
			isDRBDRTerminal,
		)
		drbdr = SetupStateUp(e, cl, drbdr)
		assertDRBDRConfigured(e, drbdr)
		assertDeviceUUIDEquals(e, drbdr, savedUUID)
		assertDeviceUUIDMatchesDisk(e, drbdr, readDevUUIDFromDisk(e, nodeExec, node.Name, backingDev))
	})
}
