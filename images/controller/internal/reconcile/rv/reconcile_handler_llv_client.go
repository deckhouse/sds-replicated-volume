package rv

import (
	"context"
	"log/slog"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// llvClientImpl implements cluster.LLVClient using a non-cached reader
type llvClientImpl struct {
	rdr client.Reader
	log *slog.Logger
}

// TODO: may be support _00000 on this level?
func (l *llvClientImpl) ByActualNamesOnTheNode(ctx context.Context, nodeName string, actualVGNameOnTheNode string, actualLVNameOnTheNode string) (*snc.LVMLogicalVolume, error) {
	l.log.Debug("LLV list start", "nodeName", nodeName, "vg", actualVGNameOnTheNode, "lv", actualLVNameOnTheNode)
	// NOTE: The LVMLogicalVolume identity fields are not indexed here; fetch and filter client-side.
	var llvList snc.LVMLogicalVolumeList
	if err := l.rdr.List(ctx, &llvList); err != nil {
		l.log.Error("LLV list failed", "nodeName", nodeName, "vg", actualVGNameOnTheNode, "lv", actualLVNameOnTheNode, "err", err)
		return nil, err
	}
	for i := range llvList.Items {
		llv := &llvList.Items[i]
		if llv.Spec.ActualLVNameOnTheNode == actualLVNameOnTheNode && llv.Spec.LVMVolumeGroupName == actualVGNameOnTheNode {
			l.log.Debug("LLV found", "name", llv.Name)
			return llv, nil
		}
	}
	l.log.Debug("LLV not found", "nodeName", nodeName, "vg", actualVGNameOnTheNode, "lv", actualLVNameOnTheNode)
	return nil, nil
}
