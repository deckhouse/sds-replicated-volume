package rv

import (
	"context"
	"log/slog"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// llvClientImpl implements cluster.LLVClient using a non-cached reader
type llvClientImpl struct {
	rdr       client.Reader
	log       *slog.Logger
	lvgByNode map[string]string
}

var _ cluster.LLVClient = &llvClientImpl{}

// TODO: may be support _00000 on this level?
func (cl *llvClientImpl) ByActualLVNameOnTheNode(
	ctx context.Context,
	nodeName string,
	actualLVNameOnTheNode string,
) (*snc.LVMLogicalVolume, error) {
	vgName, ok := cl.lvgByNode[nodeName]
	if !ok {
		cl.log.Debug("LLV not found, because VG not found for node", "nodeName", nodeName, "actualLVNameOnTheNode", actualLVNameOnTheNode)
		return nil, nil
	}

	cl.log.Debug("LLV list start", "vgName", vgName, "actualLVNameOnTheNode", actualLVNameOnTheNode)

	var llvList snc.LVMLogicalVolumeList
	if err := cl.rdr.List(ctx, &llvList); err != nil {
		cl.log.Error("LLV list failed", "vgName", vgName, "actualLVNameOnTheNode", actualLVNameOnTheNode, "err", err)
		return nil, err
	}
	for i := range llvList.Items {
		llv := &llvList.Items[i]
		if llv.Spec.LVMVolumeGroupName == vgName && llv.Spec.ActualLVNameOnTheNode == actualLVNameOnTheNode {
			cl.log.Debug("LLV found", "name", llv.Name)
			return llv, nil
		}
	}
	cl.log.Debug("LLV not found", "vgName", vgName, "actualLVNameOnTheNode", actualLVNameOnTheNode)
	return nil, nil
}
