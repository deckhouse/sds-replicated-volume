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

package framework

import (
	"context"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// PreexistingDRBDReplica holds information about a DRBD resource that
// simulates a preexisting replica from a previous control plane.
type PreexistingDRBDReplica struct {
	Type         v1alpha1.ReplicaType
	Role         v1alpha1.DRBDRole
	NodeName     string
	DRBDName     string
	NodeID       uint8
	ActualLVName string
	ActualVGName string
	LVGName      string
	ThinPoolName string
	Size         string
	Addresses    []v1alpha1.DRBDResourceAddressStatus
}

// EmulatePreexisting dismantles the RV, leaving orphaned DRBD resources on
// nodes that simulate a preexisting state from a previous control plane.
//
// It puts all DRBDRs in maintenance, demotes primaries, renames DRBD resources,
// deletes the RV (stripping LLV finalizers), and re-promotes replicas that were
// attached before the dismantle. A DeferCleanup is registered for teardown.
func (t *TestRV) EmulatePreexisting(ctx SpecContext) []PreexistingDRBDReplica {
	GinkgoHelper()
	type memberInfo struct {
		replicaType  v1alpha1.ReplicaType
		attached     bool
		nodeName     string
		oldDRBDName  string
		newDRBDName  string
		nodeID       uint8
		rvr          *TestRVR
		drbdr        *TestDRBDR
		llv          *TestLLV
		lvDevPath    string
		actualLVName string
		actualVGName string
		lvgName      string
		thinPoolName string
		size         string
		addresses    []v1alpha1.DRBDResourceAddressStatus
	}

	// --- Build attached set from datamesh members ---

	attachedNodes := make(map[string]bool)
	for _, m := range t.Object().Status.Datamesh.Members {
		if m.Attached {
			attachedNodes[m.NodeName] = true
		}
	}

	// --- Collect members from the formed RV ---

	var members []memberInfo

	for _, trvr := range t.TestRVRs() {
		obj := trvr.Object()
		drbdr := trvr.DRBDR()
		drbdrObj := drbdr.Object()

		mi := memberInfo{
			replicaType: obj.Spec.Type,
			attached:    attachedNodes[drbdrObj.Spec.NodeName],
			nodeName:    drbdrObj.Spec.NodeName,
			oldDRBDName: "sdsrv-" + drbdr.Name(),
			newDRBDName: t.f.UniqueName(),
			nodeID:      drbdrObj.Spec.NodeID,
			rvr:         trvr,
			drbdr:       drbdr,
			addresses:   drbdrObj.Status.Addresses,
		}

		if obj.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			llvs := trvr.LLVs()
			Expect(llvs).To(HaveLen(1), "expected exactly 1 LLV for diskful RVR %s", trvr.Name())
			tllv := llvs[0]
			llvObj := tllv.Object()

			var lvg snc.LVMVolumeGroup
			Expect(t.f.Client.Get(ctx, client.ObjectKey{Name: llvObj.Spec.LVMVolumeGroupName}, &lvg)).To(Succeed(),
				"getting LVMVolumeGroup %s", llvObj.Spec.LVMVolumeGroupName)

			mi.llv = tllv
			mi.lvDevPath = "/dev/" + lvg.Spec.ActualVGNameOnTheNode + "/" + llvObj.Spec.ActualLVNameOnTheNode
			mi.actualLVName = llvObj.Spec.ActualLVNameOnTheNode
			mi.actualVGName = lvg.Spec.ActualVGNameOnTheNode
			mi.lvgName = llvObj.Spec.LVMVolumeGroupName
			mi.size = llvObj.Spec.Size
			if llvObj.Spec.Thin != nil {
				mi.thinPoolName = llvObj.Spec.Thin.PoolName
			}
		}

		members = append(members, mi)
	}

	// --- DeferCleanup: tear down DRBD resources and LVs ---

	DeferCleanup(func(cleanupCtx context.Context) {
		fmt.Fprintln(GinkgoWriter, "[cleanup] preexisting DRBD: tearing down DRBD resources and LVs")

		// Group 1: check status + secondary --force (parallel per member)
		type alive struct {
			nodeName string
			drbdName string
		}
		var mu sync.Mutex
		var toClean []alive
		g, gCtx := errgroup.WithContext(cleanupCtx)
		for _, m := range members {
			g.Go(func() error {
				res, err := t.f.Drbdsetup(gCtx, m.nodeName, "status", m.newDRBDName)
				if err != nil {
					return err
				}
				if res.ExitCode != 0 {
					return nil
				}
				res, err = t.f.Drbdsetup(gCtx, m.nodeName, "secondary", "--force", m.newDRBDName)
				if err != nil {
					return err
				}
				if res.ExitCode != 0 {
					return fmt.Errorf("secondary --force %s on %s: exit %d", m.newDRBDName, m.nodeName, res.ExitCode)
				}
				mu.Lock()
				toClean = append(toClean, alive{m.nodeName, m.newDRBDName})
				mu.Unlock()
				return nil
			})
		}
		Expect(g.Wait()).To(Succeed(), "cleanup: force-secondary DRBD resources")

		// Group 2: down + poll until gone (parallel per alive)
		g, gCtx = errgroup.WithContext(cleanupCtx)
		for _, a := range toClean {
			g.Go(func() error {
				if _, err := t.f.Drbdsetup(gCtx, a.nodeName, "down", a.drbdName); err != nil {
					return err
				}
				for {
					res, err := t.f.Drbdsetup(gCtx, a.nodeName, "status", a.drbdName)
					if err != nil {
						return err
					}
					if res.ExitCode != 0 {
						return nil
					}
					select {
					case <-gCtx.Done():
						return gCtx.Err()
					case <-time.After(500 * time.Millisecond):
					}
				}
			})
		}
		Expect(g.Wait()).To(Succeed(), "cleanup: down DRBD resources")

		// Group 3: lvremove (parallel per member with lvDevPath)
		g, gCtx = errgroup.WithContext(cleanupCtx)
		for _, m := range members {
			if m.lvDevPath == "" {
				continue
			}
			g.Go(func() error {
				res, err := t.f.LVM(gCtx, m.nodeName, "lvs", m.lvDevPath)
				if err != nil {
					return err
				}
				if res.ExitCode == 0 {
					_, err = t.f.LVM(gCtx, m.nodeName, "lvremove", "-f", m.lvDevPath)
					return err
				}
				return nil
			})
		}
		Expect(g.Wait()).To(Succeed(), "cleanup: lvremove backing volumes")

		fmt.Fprintln(GinkgoWriter, "[cleanup] preexisting DRBD: done")
	})

	// --- Put ALL DRBDRs in maintenance ---

	for i := range members {
		members[i].drbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
			d.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation
		})
	}
	for _, m := range members {
		m.drbdr.Await(ctx, tkmatch.ConditionReason(
			v1alpha1.DRBDResourceCondConfiguredType,
			v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance))
	}

	// --- Demote all primaries before rename (parallel) ---
	// RVA creation (Access + extra attach) automatically promotes replicas.
	// All must be demoted before the RV can be deleted.

	g, gCtx := errgroup.WithContext(ctx)
	for _, m := range members {
		g.Go(func() error {
			_, err := t.f.Drbdsetup(gCtx, m.nodeName, "secondary", m.oldDRBDName)
			return err
		})
	}
	Expect(g.Wait()).To(Succeed(), "demoting DRBD primaries")

	fmt.Fprintln(GinkgoWriter, "[emulate] waiting for attached replicas to detach after demotion")
	for _, m := range members {
		if m.attached {
			m.rvr.Await(ctx, tkmatch.ConditionStatus(
				v1alpha1.ReplicatedVolumeReplicaCondAttachedType, "False"))
		}
	}

	// --- Rename ALL DRBD resources (parallel) ---

	g, gCtx = errgroup.WithContext(ctx)
	for _, m := range members {
		g.Go(func() error {
			res, err := t.f.Drbdsetup(gCtx, m.nodeName, "rename-resource", m.oldDRBDName, m.newDRBDName)
			if err != nil {
				return err
			}
			if res.ExitCode != 0 {
				return fmt.Errorf("rename %s -> %s on %s: exit %d", m.oldDRBDName, m.newDRBDName, m.nodeName, res.ExitCode)
			}
			return nil
		})
	}
	Expect(g.Wait()).To(Succeed(), "renaming DRBD resources")

	// --- Force-delete attached DRBDRs ---
	// Attached DRBDRs (primary) block RV deletion because the datamesh engine
	// refuses to remove a member whose DRBDR still exists. We delete them and
	// strip finalizers so they disappear immediately.
	//
	// We re-Get the object inside a retry-on-conflict loop because the agent
	// may concurrently update status (e.g. observed DRBD state changes during
	// teardown) and bump the resourceVersion between our Get and our Update.
	// NotFound is treated as success (controller may have cleaned up first);
	// any other error fails the test rather than silently leaving a stuck
	// finalizer.
	for _, m := range members {
		if !m.attached {
			continue
		}
		m.drbdr.Delete(ctx)

		drbdrName := m.drbdr.Name()
		err := retry.OnError(retry.DefaultRetry, apierrors.IsConflict, func() error {
			latest := &v1alpha1.DRBDResource{}
			if err := t.f.Client.Get(ctx, client.ObjectKey{Name: drbdrName}, latest); err != nil {
				return err
			}
			latest.SetFinalizers(nil)
			// SudoClient: the DRBDResource finalizer contains "deckhouse.io",
			// so the deny-deckhouse-finalizers ValidatingAdmissionPolicy would
			// block a plain Update. Impersonation of system:sudouser bypasses
			// it, matching the `kubectl --as=system:sudouser` recipe.
			err := t.f.SudoClient.Update(ctx, latest)
			if apierrors.IsConflict(err) {
				fmt.Fprintf(GinkgoWriter,
					"[%s] [emulate] strip finalizers on DRBDR %s: conflict, retrying\n",
					time.Now().Format("15:04:05.000"), drbdrName)
			}
			return err
		})
		Expect(client.IgnoreNotFound(err)).To(Succeed(),
			"stripping finalizers on DRBDR %s", drbdrName)
	}

	// --- Delete all RVAs and RVRs ---

	for _, trva := range t.rvas.All() {
		trva.Delete(ctx)
	}
	for _, trvr := range t.TestRVRs() {
		trvr.Delete(ctx)
	}

	// --- Clear datamesh status and delete RV ---

	t.Delete(ctx)

	Expect(t.f.Client.Status().Patch(ctx, t.Object(),
		client.RawPatch(types.MergePatchType, []byte(
			`{"status":{"datamesh":{"members":[]},"datameshTransitions":null,"datameshReplicaRequests":null}}`,
		)),
	)).To(Succeed(), "clearing datamesh status on RV %s", t.Name())

	// Remove all finalizers from LLVs so they can be deleted from Kubernetes
	// even though the underlying LV is still held open by the renamed DRBD
	// resource. sds-node-configurator cannot lvremove a busy LV, so its
	// finalizer would normally block deletion indefinitely. This recreates the
	// "LVM device exists, but no LLV object" state expected by the adopt test.
	//
	// We first wait for each LLV to start deleting (the controller has
	// processed the RVR deletion and requested LLV removal) — without this
	// wait we may race with the controller and see stale LLVs. The strip
	// itself goes through a raw client with NotFound tolerated: under
	// parallel load sds-node-configurator sometimes completes the deletion
	// on its own first, and losing that race is fine — the LLV object being
	// gone is exactly the state this step is after. What must NOT be assumed
	// away is the on-disk LV; its survival is verified explicitly below.
	for _, m := range members {
		if m.llv == nil {
			continue
		}
		m.llv.Await(ctx, tkmatch.IsDeleting())

		// Merge-patch, not Get+Update: it carries no resourceVersion, so it
		// cannot conflict with sds-node-configurator's concurrent updates of
		// the same LLV (phase/finalizer churn is heavy under parallel load).
		llvName := m.llv.Name()
		target := &snc.LVMLogicalVolume{}
		target.SetName(llvName)
		// SudoClient: the LLV finalizer contains "deckhouse.io", so the
		// deny-deckhouse-finalizers ValidatingAdmissionPolicy would block a
		// plain Patch. Impersonation of system:sudouser bypasses it, matching
		// the `kubectl --as=system:sudouser` recipe.
		err := t.f.SudoClient.Patch(ctx, target,
			client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`)))
		Expect(client.IgnoreNotFound(err)).To(Succeed(),
			"stripping finalizers on LLV %s", llvName)

		m.llv.Await(ctx, tkmatch.Deleted())
	}

	t.Await(ctx, tkmatch.Deleted())

	// The adopt scenario's precondition is "the LV exists on disk, but no
	// object references it". The strip above guarantees the second half;
	// verify the first half instead of assuming it — if anything ever manages
	// to remove the busy LV (or to skip removal while still dropping the LLV,
	// which would orphan nothing here but break the scenario), the spec must
	// fail at this point with an explicit message, not later with a
	// mysterious adopt failure.
	for _, m := range members {
		if m.lvDevPath == "" {
			continue
		}
		res, err := t.f.LVM(ctx, m.nodeName, "lvs", m.lvDevPath)
		Expect(err).To(Succeed(), "checking preexisting LV %s on %s", m.lvDevPath, m.nodeName)
		Expect(res.ExitCode).To(Equal(0),
			"preexisting LV %s vanished from node %s: the emulated state requires the LV to survive the LLV deletion",
			m.lvDevPath, m.nodeName)
	}

	// --- Re-promote replicas that were attached (parallel) ---

	g, gCtx = errgroup.WithContext(ctx)
	for i, m := range members {
		if !m.attached {
			continue
		}
		g.Go(func() error {
			const maxAttempts = 3
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				res, err := t.f.Drbdsetup(gCtx, m.nodeName, "primary", m.newDRBDName)
				if err != nil {
					return err
				}
				if res.ExitCode == 0 {
					members[i].attached = true
					return nil
				}
				if attempt == maxAttempts {
					return fmt.Errorf("primary %s on %s: exit %d (after %d attempts)", m.newDRBDName, m.nodeName, res.ExitCode, maxAttempts)
				}
				select {
				case <-gCtx.Done():
					return gCtx.Err()
				case <-time.After(time.Duration(attempt) * time.Second):
				}
			}
			return nil
		})
	}
	Expect(g.Wait()).To(Succeed(), "re-promoting attached replicas")

	// --- Build result ---

	result := make([]PreexistingDRBDReplica, len(members))
	for i, m := range members {
		role := v1alpha1.DRBDRoleSecondary
		if m.attached {
			role = v1alpha1.DRBDRolePrimary
		}
		result[i] = PreexistingDRBDReplica{
			Type:         m.replicaType,
			Role:         role,
			NodeName:     m.nodeName,
			DRBDName:     m.newDRBDName,
			NodeID:       m.nodeID,
			ActualLVName: m.actualLVName,
			ActualVGName: m.actualVGName,
			LVGName:      m.lvgName,
			ThinPoolName: m.thinPoolName,
			Size:         m.size,
			Addresses:    m.addresses,
		}
	}
	return result
}
