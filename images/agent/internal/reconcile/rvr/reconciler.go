package rvr

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
	v9 "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf/v9"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var resourcesDir = "/var/lib/sds-replicated-volume-agent.d/"

type Reconciler struct {
	log *slog.Logger
	cl  client.Client
}

func NewReconciler(log *slog.Logger, cl client.Client) *Reconciler {
	return &Reconciler{
		log: log,
		cl:  cl,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req Request,
) (reconcile.Result, error) {

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

	resourceCfg := createResourceConfig(rvr)

	resourceSection := &drbdconf.Section{}

	if err = drbdconf.Marshal(resourceCfg, resourceSection); err != nil {
		return fmt.Errorf("marshaling resource %s cfg: %w", req.Name, err)
	}

	root := &drbdconf.Root{
		Elements: []drbdconf.RootElement{resourceSection},
	}

	filepath := filepath.Join(resourcesDir, req.Name+".res")

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

	// drbdconf.Unmarshal[]()

	// create res file, if not exist
	// parse res file
	// update resource
	//
	// drbdadm adjust, if needed
	// drbdadm up, if needed
	return nil
}

func createResourceConfig(rvr *v1alpha2.ReplicatedVolumeReplica) *v9.Resource {
	res := &v9.Resource{
		Name: rvr.Name,
		Net: &v9.Net{
			Protocol: v9.ProtocolC,
		},
	}

	for peerName, peer := range rvr.Spec.Peers {
		res.On = append(res.On, &v9.On{
			HostNames: []string{},
			// NodeId: ,
		})
	}

	return res
}

// resource test {
//     on T14 {
//         node-id 0;
//         device           /dev/drbd0 minor 0;
//         disk             /dev/loop40;
//         meta-disk        internal;
//         address          ipv4 127.0.0.1:7788;
//     }
//     on a-stefurishin-master-0 {
//         node-id 1;
//         device           /dev/drbd0 minor 0;
//         disk             /dev/loop41;
//         meta-disk        internal;
//         address          ipv4 127.0.0.1:7789;
//     }
//     net {
//         protocol           C;
//     }
// }
