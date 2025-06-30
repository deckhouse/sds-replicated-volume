package rvr

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	. "github.com/deckhouse/sds-replicated-volume/images/agent/internal/utils"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
	v9 "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf/v9"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var resourcesDir = "/var/lib/sds-replicated-volume-agent.d/"

type Reconciler struct {
	log      *slog.Logger
	cl       client.Client
	nodeName string
}

func NewReconciler(log *slog.Logger, cl client.Client, nodeName string) *Reconciler {
	return &Reconciler{
		log:      log,
		cl:       cl,
		nodeName: nodeName,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req Request,
) (reconcile.Result, error) {
	r.log.Debug("reconciling", "type", reflect.TypeOf(req).String())

	var err error
	switch typedReq := req.(type) {
	case ResourceReconcileRequest:
		err = r.handleResourceReconcile(ctx, typedReq)

	default:
		r.log.Error("unknown req type", "type", reflect.TypeOf(req).String())
		return reconcile.Result{}, nil
	}

	return reconcile.Result{}, err
}

func (r *Reconciler) handleResourceReconcile(ctx context.Context, req ResourceReconcileRequest) error {
	rvr := &v1alpha2.ReplicatedVolumeReplica{}
	err := r.cl.Get(ctx, client.ObjectKey{Name: req.Name}, rvr)
	if err != nil {
		return fmt.Errorf("getting rvr %s: %w", req.Name, err)
	}

	if err := r.writeResourceConfig(rvr); err != nil {
		return err
	}

	exists, err := drbdadm.ExecuteDumpMD_MetadataExists(ctx, rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		return fmt.Errorf("ExecuteDumpMD_MetadataExists: %w", err)
	}

	if !exists {
		if err := drbdadm.ExecuteCreateMD(ctx, rvr.Spec.ReplicatedVolumeName); err != nil {
			return fmt.Errorf("ExecuteCreateMD: %w", err)
		}

		r.log.Info("successfully created metadata for 'resource'", "resource", rvr.Spec.ReplicatedVolumeName)
	}

	isUp, err := drbdadm.ExecuteStatus_IsUp(ctx, rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		return fmt.Errorf("ExecuteStatus_IsUp: %w", err)
	}

	if !isUp {
		if err := drbdadm.ExecuteUp(ctx, rvr.Spec.ReplicatedVolumeName); err != nil {
			return fmt.Errorf("ExecuteUp: %w", err)
		}

		r.log.Info("successfully upped 'resource'", "resource", rvr.Spec.ReplicatedVolumeName)
	}

	if err := drbdadm.ExecuteAdjust(ctx, rvr.Spec.ReplicatedVolumeName); err != nil {
		return fmt.Errorf("ExecuteAdjust: %w", err)
	}

	r.log.Info("successfully adjusted 'resource'", "resource", rvr.Spec.ReplicatedVolumeName)

	return nil
}

func (r *Reconciler) writeResourceConfig(rvr *v1alpha2.ReplicatedVolumeReplica) error {
	resourceCfg := r.generateResourceConfig(rvr)

	resourceSection := &drbdconf.Section{}

	if err := drbdconf.Marshal(resourceCfg, resourceSection); err != nil {
		return fmt.Errorf("marshaling resource %s cfg: %w", rvr.Spec.ReplicatedVolumeName, err)
	}

	root := &drbdconf.Root{
		Elements: []drbdconf.RootElement{resourceSection},
	}

	filepath := filepath.Join(resourcesDir, rvr.Spec.ReplicatedVolumeName+".res")

	file, err := os.OpenFile(filepath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open file %s: %w", filepath, err)
	}

	defer file.Close()

	n, err := root.WriteTo(file)
	if err != nil {
		return fmt.Errorf("writing file %s: %w", filepath, err)
	}

	r.log.Info("successfully wrote 'n' bytes to 'file'", "n", n, "file", filepath)
	return nil
}

func (r *Reconciler) generateResourceConfig(rvr *v1alpha2.ReplicatedVolumeReplica) *v9.Config {
	res := &v9.Resource{
		Name: rvr.Spec.ReplicatedVolumeName,
		Net: &v9.Net{
			Protocol: v9.ProtocolC,
		},
	}

	for peerName, peer := range rvr.Spec.Peers {
		onSection := &v9.On{
			HostNames: []string{peerName},
			NodeId:    Ptr(peer.NodeId),
			Address: &v9.AddressWithPort{
				Address:       peer.Address.IPv4,
				Port:          peer.Address.Port,
				AddressFamily: "ipv4",
			},
		}
		res.On = append(res.On, onSection)

		// add volumes for current node
		if peerName == r.nodeName {
			for _, volume := range rvr.Spec.Volumes {
				vol := &v9.Volume{
					Number:   Ptr(int(volume.Number)),
					Device:   Ptr(v9.DeviceMinorNumber(volume.DeviceMinorNumber)),
					Disk:     Ptr(v9.VolumeDisk(volume.Disk)),
					MetaDisk: &v9.VolumeMetaDiskInternal{},
				}
				onSection.Volumes = append(onSection.Volumes, vol)
			}
		}
	}

	return &v9.Config{
		Resources: []*v9.Resource{res},
	}
}
