package rv

import (
	"context"
	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// nodeRVRClientImpl implements cluster.NodeRVRClient using a non-cached reader
type nodeRVRClientImpl struct {
	rdr client.Reader
	log *slog.Logger
}

func (r *nodeRVRClientImpl) ByNodeName(ctx context.Context, nodeName string) ([]v1alpha2.ReplicatedVolumeReplica, error) {
	r.log.Debug("RVR list by node start", "nodeName", nodeName)
	var list v1alpha2.ReplicatedVolumeReplicaList
	err := r.rdr.List(
		ctx,
		&list,
		client.MatchingFields{"spec.nodeName": nodeName},
	)
	if err != nil {
		r.log.Error("RVR list by node failed", "nodeName", nodeName, "err", err)
		return nil, err
	}
	r.log.Debug("RVR list by node done", "nodeName", nodeName, "count", len(list.Items))
	return list.Items, nil
}
