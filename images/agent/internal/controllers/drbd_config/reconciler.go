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

package drbdconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/agent/internal/errors"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
	v9 "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf/v9"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
	capi "github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

type Reconciler struct {
	cl       client.Client
	rdr      client.Reader
	sch      *runtime.Scheme
	log      *slog.Logger
	nodeName string
}

var _ reconcile.TypedReconciler[Request] = &Reconciler{}

var resourcesDir = "/var/lib/sds-replicated-volume-agent.d/"

func (r *Reconciler) OnRVRCreateOrUpdate(
	ctx context.Context,
	rvr *v1alpha3.ReplicatedVolumeReplica,
	q TQueue,
) {
	if !r.rvrOnThisNode(rvr) {
		return
	}

	if rvr.DeletionTimestamp != nil {
		if slices.Contains(rvr.Finalizers, Finalizer) {
			r.log.Info("deletionTimestamp, do cleanup")
			q.Add(DownRequest{RVRName: rvr.Name})
		} else {
			r.log.Info("deletionTimestamp, but no finalizer - skip cleanup")
		}
		return
	}

	if !r.rvrInitialized(ctx, rvr) {
		r.log.Debug("rvr not initialized - skip")
		return
	}

	r.log.Debug("up resource", "rvrName", rvr.Name)

	q.Add(UpRequest{RVRName: rvr.Name})
}

func (r *Reconciler) rvrOnThisNode(rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	if rvr.Spec.NodeName == "" {
		return false
	}
	if rvr.Spec.NodeName != r.nodeName {
		r.log.Debug("invalid node - skip",
			"rvrName", rvr.Name,
			"rvrNodeName", rvr.Spec.NodeName,
			"nodeName", r.nodeName,
		)
		return false
	}
	return true
}

func (r *Reconciler) rvrInitialized(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	if rvr.Spec.ReplicatedVolumeName == "" {
		return false
	}
	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
		return false
	}
	if rvr.Status.DRBD.Config.NodeId == nil {
		return false
	}
	if rvr.Status.DRBD.Config.Address == nil {
		return false
	}
	if !rvr.Status.DRBD.Config.PeersInitialized {
		return false
	}
	if rvr.Spec.Type == "Diskful" && rvr.Status.LVMLogicalVolumeName == "" {
		return false
	}
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.rdr.Get(ctx, client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}, rv); err != nil {
		return false
	}
	if rv.Status == nil || rv.Status.DRBD == nil || rv.Status.DRBD.Config == nil {
		return false
	}
	if rv.Status.DRBD.Config.SharedSecret == "" || rv.Status.DRBD.Config.SharedSecretAlg == "" {
		return false
	}
	return true
}

func (r *Reconciler) Reconcile(
	_ context.Context,
	req Request,
) (reconcile.Result, error) {
	switch typedReq := req.(type) {
	case UpRequest:
		return r.handleUp(typedReq.RVRName)
	case DownRequest:
		return r.handleDown(typedReq.RVRName)
	default:
		r.log.Error("unknown req type", "typedReq", typedReq)
		return reconcile.Result{}, e.ErrNotImplemented
	}
}

func (r *Reconciler) handleUp(name string) (reconcile.Result, error) {
	ctx := context.Background()

	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rvr); err != nil {
		return reconcile.Result{}, fmt.Errorf("get rvr: %w", err)
	}
	if !r.rvrOnThisNode(rvr) {
		return reconcile.Result{}, nil
	}
	if !r.rvrInitialized(ctx, rvr) {
		r.log.Debug("rvr not initialized - skip", "rvrName", rvr.Name)
		return reconcile.Result{}, nil
	}

	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}, rv); err != nil {
		return reconcile.Result{}, fmt.Errorf("get rv: %w", err)
	}

	// ensure finalizer
	if err := capi.PatchWithConflictRetry(ctx, r.cl, rvr, func(obj *v1alpha3.ReplicatedVolumeReplica) error {
		if slices.Contains(obj.Finalizers, Finalizer) {
			return nil
		}
		obj.Finalizers = append(obj.Finalizers, Finalizer)
		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("ensure finalizer: %w", err)
	}

	// ensure status structs
	if err := capi.PatchStatusWithConflictRetry(ctx, r.cl, rvr, func(obj *v1alpha3.ReplicatedVolumeReplica) error {
		if obj.Status == nil {
			obj.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}
		if obj.Status.DRBD == nil {
			obj.Status.DRBD = &v1alpha3.DRBD{}
		}
		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("init status: %w", err)
	}

	// write and validate config
	initialSyncPassed := rvr.Status.DRBD.Actual != nil && rvr.Status.DRBD.Actual.InitialSyncCompleted
	if err := r.writeAndTestResourceConfig(ctx, rvr, rv, initialSyncPassed); err != nil {
		_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonConfigurationFailed, "config validation failed: "+err.Error())
		return reconcile.Result{}, fmt.Errorf("write/test cfg: %w", err)
	}

	// set actual fields: allowTwoPrimaries and disk path
	if err := capi.PatchStatusWithConflictRetry(ctx, r.cl, rvr, func(obj *v1alpha3.ReplicatedVolumeReplica) error {
		if obj.Status.DRBD.Actual == nil {
			obj.Status.DRBD.Actual = &v1alpha3.DRBDActual{}
		}
		obj.Status.DRBD.Actual.AllowTwoPrimaries = rv.Status.DRBD.Config.AllowTwoPrimaries
		if obj.Spec.Type == "Diskful" && obj.Status.LVMLogicalVolumeName != "" {
			obj.Status.DRBD.Actual.Disk = obj.Status.LVMLogicalVolumeName
		} else {
			obj.Status.DRBD.Actual.Disk = ""
		}
		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("set actual: %w", err)
	}

	// Diskful path: ensure metadata; maybe do initial sync
	if rvr.Spec.Type == "Diskful" {
		exists, err := drbdadm.ExecuteDumpMDMetadataExists(ctx, rvr.Spec.ReplicatedVolumeName)
		if err != nil {
			_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonMetadataCheckFailed, "dump-md failed: "+err.Error())
			return reconcile.Result{}, fmt.Errorf("dump-md: %w", err)
		}
		if !exists {
			_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeInitialSync, metav1.ConditionFalse, v1alpha3.ReasonInitialSyncRequiredButNotReady, "Creating metadata needed for initial sync")
			if err := drbdadm.ExecuteCreateMD(ctx, rvr.Spec.ReplicatedVolumeName); err != nil {
				_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonMetadataCreationFailed, "create-md failed: "+err.Error())
				return reconcile.Result{}, fmt.Errorf("create-md: %w", err)
			}
			_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeInitialSync, metav1.ConditionFalse, v1alpha3.ReasonSafeForInitialSync, "Initial synchronization may be performed")
		}
		needInitialSync := r.initialSyncRequired(rvr)
		if needInitialSync && !(rvr.Status.DRBD.Actual != nil && rvr.Status.DRBD.Actual.InitialSyncCompleted) {
			if err := drbdadm.ExecutePrimaryForce(ctx, rvr.Spec.ReplicatedVolumeName); err != nil {
				_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonPromotionDemotionFailed, "primary --force failed: "+err.Error())
				return reconcile.Result{}, fmt.Errorf("primary --force: %w", err)
			}
			if err := drbdadm.ExecuteSecondary(ctx, rvr.Spec.ReplicatedVolumeName); err != nil {
				_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonPromotionDemotionFailed, "secondary failed: "+err.Error())
				return reconcile.Result{}, fmt.Errorf("secondary: %w", err)
			}
			if err := r.setInitialSyncCompleted(ctx, rvr, true); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		if err := r.setInitialSyncCompleted(ctx, rvr, true); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Ensure up
	isUp, err := drbdadm.ExecuteStatusIsUp(ctx, rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonStatusCheckFailed, "status failed: "+err.Error())
		return reconcile.Result{}, fmt.Errorf("status: %w", err)
	}
	if !isUp {
		if err := drbdadm.ExecuteUp(ctx, rvr.Spec.ReplicatedVolumeName); err != nil {
			_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonResourceUpFailed, "up failed: "+err.Error())
			return reconcile.Result{}, fmt.Errorf("up: %w", err)
		}
	}

	// Adjust
	if err := drbdadm.ExecuteAdjust(ctx, rvr.Spec.ReplicatedVolumeName); err != nil {
		_ = r.saveLastAdjustmentError(ctx, rvr, err)
		_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonConfigurationAdjustFailed, "adjust failed: "+err.Error())
		return reconcile.Result{}, fmt.Errorf("adjust: %w", err)
	}
	_ = r.clearLastAdjustmentError(ctx, rvr)

	// If initial sync not finished, pause configuration
	initialSyncCompleted := rvr.Status.DRBD.Actual != nil && rvr.Status.DRBD.Actual.InitialSyncCompleted
	if !initialSyncCompleted {
		_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonConfigurationAdjustmentPausedUntilInitialSync, "waiting for initial sync")
		return reconcile.Result{}, nil
	}

	// Post-InitialSync: primary/secondary
	if err := r.handlePrimarySecondary(ctx, rvr); err != nil {
		_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionFalse, v1alpha3.ReasonPromotionDemotionFailed, "promotion/demotion failed: "+err.Error())
		return reconcile.Result{}, fmt.Errorf("primary/secondary: %w", err)
	}

	// success
	_ = r.setCondition(ctx, rvr, v1alpha3.ConditionTypeConfigurationAdjusted, metav1.ConditionTrue, v1alpha3.ReasonConfigurationAdjustmentSucceeded, "Replica is configured")
	return reconcile.Result{}, nil
}

func (r *Reconciler) handleDown(name string) (reconcile.Result, error) {
	ctx := context.Background()
	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rvr); err != nil {
		return reconcile.Result{}, fmt.Errorf("get rvr: %w", err)
	}
	if !r.rvrOnThisNode(rvr) {
		return reconcile.Result{}, nil
	}
	// if there are other finalizers (besides ours) — skip cleanup
	for _, fz := range rvr.Finalizers {
		if fz != Finalizer {
			r.log.Debug("other finalizers present, skip drbd-config cleanup", "finalizer", fz)
			return reconcile.Result{}, nil
		}
	}
	// drbd down
	if err := drbdadm.ExecuteDown(ctx, rvr.Spec.ReplicatedVolumeName); err != nil {
		r.log.Error("drbd down failed", "error", err)
	}
	// remove config files
	_ = os.Remove(filepath.Join(resourcesDir, rvr.Spec.ReplicatedVolumeName+".res"))
	_ = os.Remove(filepath.Join(resourcesDir, rvr.Spec.ReplicatedVolumeName+".res_tmp"))
	// remove our finalizer
	if err := capi.PatchWithConflictRetry(ctx, r.cl, rvr, func(obj *v1alpha3.ReplicatedVolumeReplica) error {
		var fzs []string
		for _, fz := range obj.Finalizers {
			if fz != Finalizer {
				fzs = append(fzs, fz)
			}
		}
		obj.Finalizers = fzs
		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return reconcile.Result{}, nil
}

func (r *Reconciler) initialSyncRequired(rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
		return false
	}
	if !rvr.Status.DRBD.Config.PeersInitialized || len(rvr.Status.DRBD.Config.Peers) != 0 {
		return false
	}
	if rvr.Status.DRBD.Actual != nil && rvr.Status.DRBD.Actual.InitialSyncCompleted {
		return false
	}
	if rvr.Status.DRBD.Status == nil || len(rvr.Status.DRBD.Status.Devices) == 0 {
		return false
	}
	return rvr.Status.DRBD.Status.Devices[0].DiskState != "UpToDate"
}

func (r *Reconciler) writeAndTestResourceConfig(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica, rv *v1alpha3.ReplicatedVolume, initialSyncPassed bool) error {
	sec := &drbdconf.Section{}
	if err := drbdconf.Marshal(&v9.Config{Resources: []*v9.Resource{r.generateResourceConfig(rvr, rv, initialSyncPassed)}}, sec); err != nil {
		return fmt.Errorf("marshal resource: %w", err)
	}
	root := &drbdconf.Root{}
	for _, el := range sec.Elements {
		root.Elements = append(root.Elements, el.(*drbdconf.Section))
	}
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmpPath := filepath.Join(resourcesDir, rvr.Spec.ReplicatedVolumeName+".res_tmp")
	mainPath := filepath.Join(resourcesDir, rvr.Spec.ReplicatedVolumeName+".res")
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	if _, err := root.WriteTo(tmp); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	_ = tmp.Close()
	// test
	args := []string{"--config-to-test", tmpPath, "--config-to-exclude", mainPath, "sh-nop"}
	cmd := exec.CommandContext(ctx, drbdadm.Command, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return errors.Join(err, errors.New(string(out)))
	}
	// write main
	main, err := os.OpenFile(mainPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open main: %w", err)
	}
	if _, err := root.WriteTo(main); err != nil {
		_ = main.Close()
		return fmt.Errorf("write main: %w", err)
	}
	_ = main.Close()
	_ = os.Remove(tmpPath)
	return nil
}

func (r *Reconciler) generateResourceConfig(rvr *v1alpha3.ReplicatedVolumeReplica, rv *v1alpha3.ReplicatedVolume, initialSyncPassed bool) *v9.Resource {
	res := &v9.Resource{
		Name: rvr.Spec.ReplicatedVolumeName,
		Net: &v9.Net{
			Protocol:          v9.ProtocolC,
			SharedSecret:      rv.Status.DRBD.Config.SharedSecret,
			RRConflict:        v9.RRConflictPolicyRetryConnect,
			AllowTwoPrimaries: rv.Status.DRBD.Config.AllowTwoPrimaries,
		},
		Options: &v9.Options{
			OnNoQuorum:                 v9.OnNoQuorumPolicySuspendIO,
			OnNoDataAccessible:         v9.OnNoDataAccessiblePolicySuspendIO,
			OnSuspendedPrimaryOutdated: v9.OnSuspendedPrimaryOutdatedPolicyForceSecondary,
			AutoPromote:                ptr(false),
		},
	}
	// current node
	on := &v9.On{HostNames: []string{r.nodeName}, NodeID: ptr(uint(*rvr.Status.DRBD.Config.NodeId))}
	vol := &v9.Volume{
		Number: ptr(0),
		Device: ptr(v9.DeviceMinorNumber(rv.Status.DRBD.Config.DeviceMinor)),
		DiskOptions: &v9.DiskOptions{
			DiscardZeroesIfAligned: ptr(false),
			RsDiscardGranularity:   ptr(uint(8192)),
		},
		MetaDisk: &v9.VolumeMetaDiskInternal{},
	}
	if rvr.Spec.Type == "Diskful" && rvr.Status.LVMLogicalVolumeName != "" {
		d := v9.VolumeDisk(rvr.Status.LVMLogicalVolumeName)
		vol.Disk = &d
	} else {
		vol.Disk = &v9.VolumeDiskNone{}
	}
	on.Volumes = append(on.Volumes, vol)
	res.On = append(res.On, on)
	// peers
	for peerName, peer := range rvr.Status.DRBD.Config.Peers {
		onPeer := &v9.On{
			HostNames: []string{peerName},
			NodeID:    ptr(uint(peer.NodeId)),
		}
		pv := &v9.Volume{
			Number: ptr(0),
			Device: ptr(v9.DeviceMinorNumber(rv.Status.DRBD.Config.DeviceMinor)),
			DiskOptions: &v9.DiskOptions{
				DiscardZeroesIfAligned: ptr(false),
				RsDiscardGranularity:   ptr(uint(8192)),
			},
			MetaDisk: &v9.VolumeMetaDiskInternal{},
		}
		if peer.Diskless {
			pv.Disk = &v9.VolumeDiskNone{}
		} else {
			d := v9.VolumeDisk("/not/used")
			pv.Disk = &d
		}
		onPeer.Volumes = append(onPeer.Volumes, pv)
		res.On = append(res.On, onPeer)

		con := &v9.Connection{
			Hosts: []v9.HostAddress{
				{
					Name:            r.nodeName,
					AddressWithPort: fmt.Sprintf("%s:%d", rvr.Status.DRBD.Config.Address.IPv4, rvr.Status.DRBD.Config.Address.Port),
					AddressFamily:   "ipv4",
				},
				{
					Name:            peerName,
					AddressWithPort: fmt.Sprintf("%s:%d", peer.Address.IPv4, peer.Address.Port),
					AddressFamily:   "ipv4",
				},
			},
		}
		res.Connections = append(res.Connections, con)
	}
	if initialSyncPassed {
		if rv.Status.DRBD.Config.Quorum == 0 {
			res.Options.Quorum = &v9.QuorumOff{}
		} else {
			res.Options.Quorum = &v9.QuorumNumeric{Value: int(rv.Status.DRBD.Config.Quorum)}
		}
		if rv.Status.DRBD.Config.QuorumMinimumRedundancy == 0 {
			res.Options.QuorumMinimumRedundancy = &v9.QuorumMinimumRedundancyOff{}
		} else {
			res.Options.QuorumMinimumRedundancy = &v9.QuorumMinimumRedundancyNumeric{Value: int(rv.Status.DRBD.Config.QuorumMinimumRedundancy)}
		}
	}
	return res
}

func (r *Reconciler) handlePrimarySecondary(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica) error {
	status, err := drbdsetup.ExecuteStatus(ctx)
	if err != nil {
		return fmt.Errorf("drbdsetup status: %w", err)
	}
	var currentRole string
	for _, res := range status {
		if res.Name == rvr.Spec.ReplicatedVolumeName {
			currentRole = res.Role
			break
		}
	}
	if currentRole == "" {
		return fmt.Errorf("resource %s not found in DRBD status", rvr.Spec.ReplicatedVolumeName)
	}
	desiredPrimary := false
	if rvr.Status.DRBD.Config != nil && rvr.Status.DRBD.Config.Primary != nil {
		desiredPrimary = *rvr.Status.DRBD.Config.Primary
	}
	if desiredPrimary && currentRole != "Primary" {
		return drbdadm.ExecutePrimary(ctx, rvr.Spec.ReplicatedVolumeName)
	}
	if !desiredPrimary && currentRole == "Primary" {
		return drbdadm.ExecuteSecondary(ctx, rvr.Spec.ReplicatedVolumeName)
	}
	return nil
}

func (r *Reconciler) setCondition(
	ctx context.Context,
	rvr *v1alpha3.ReplicatedVolumeReplica,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	return capi.PatchStatusWithConflictRetry(ctx, r.cl, rvr, func(obj *v1alpha3.ReplicatedVolumeReplica) error {
		if obj.Status == nil {
			obj.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    condType,
			Status:  status,
			Reason:  reason,
			Message: message,
		})
		return nil
	})
}

func (r *Reconciler) saveLastAdjustmentError(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica, err error) error {
	var exitErr *exec.ExitError
	exitCode := 0
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	return capi.PatchStatusWithConflictRetry(ctx, r.cl, rvr, func(obj *v1alpha3.ReplicatedVolumeReplica) error {
		if obj.Status == nil {
			obj.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}
		if obj.Status.DRBD == nil {
			obj.Status.DRBD = &v1alpha3.DRBD{}
		}
		if obj.Status.DRBD.Errors == nil {
			obj.Status.DRBD.Errors = &v1alpha3.DRBDErrors{}
		}
		obj.Status.DRBD.Errors.LastAdjustmentError = &v1alpha3.CmdError{
			Output:   err.Error(),
			ExitCode: exitCode,
		}
		return nil
	})
}

func (r *Reconciler) clearLastAdjustmentError(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica) error {
	return capi.PatchStatusWithConflictRetry(ctx, r.cl, rvr, func(obj *v1alpha3.ReplicatedVolumeReplica) error {
		if obj.Status != nil && obj.Status.DRBD != nil && obj.Status.DRBD.Errors != nil {
			obj.Status.DRBD.Errors.LastAdjustmentError = nil
		}
		return nil
	})
}

func (r *Reconciler) setInitialSyncCompleted(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica, v bool) error {
	return capi.PatchStatusWithConflictRetry(ctx, r.cl, rvr, func(obj *v1alpha3.ReplicatedVolumeReplica) error {
		if obj.Status == nil {
			obj.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}
		if obj.Status.DRBD == nil {
			obj.Status.DRBD = &v1alpha3.DRBD{}
		}
		if obj.Status.DRBD.Actual == nil {
			obj.Status.DRBD.Actual = &v1alpha3.DRBDActual{}
		}
		obj.Status.DRBD.Actual.InitialSyncCompleted = v
		return nil
	})
}

func ptr[T any](v T) *T { return &v }
