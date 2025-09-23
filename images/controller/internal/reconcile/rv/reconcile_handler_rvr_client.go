package rv

import (
	"context"
	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// rvrClientImpl implements cluster.RVRClient using a non-cached reader
type rvrClientImpl struct {
	rdr client.Reader
	log *slog.Logger
}

func (r *rvrClientImpl) ByReplicatedVolumeName(ctx context.Context, resourceName string) ([]v1alpha2.ReplicatedVolumeReplica, error) {
	r.log.Debug("RVR list start", "replicatedVolumeName", resourceName)
	var list v1alpha2.ReplicatedVolumeReplicaList
	err := r.rdr.List(
		ctx,
		&list,
		client.MatchingFields{"spec.replicatedVolumeName": resourceName},
	)
	if err != nil {
		r.log.Error("RVR list failed", "replicatedVolumeName", resourceName, "err", err)
		return nil, err
	}
	r.log.Debug("RVR list done", "replicatedVolumeName", resourceName, "count", len(list.Items))
	return list.Items, nil
}
