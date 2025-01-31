package scheduler

import (
	"encoding/json"
	"errors"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
)

const restoreAnnotation = "stork.libopenstorage.org/restore-in-progress"

func (s *scheduler) Filter(w http.ResponseWriter, r *http.Request) {
	s.log.Debug("[filter] starts the serving")

	decoder := json.NewDecoder(r.Body)
	defer func() {
		if err := r.Body.Close(); err != nil {
			s.log.Error(err, "Error closing decoder")
		}
	}()

	var inputData ExtenderArgs
	if err := decoder.Decode(&inputData); err != nil {
		s.log.Error(err, "[filter] unable to decode a request")
		http.Error(w, "Decode error", http.StatusBadRequest)
		return
	}

	if inputData.Pod == nil {
		s.log.Error(errors.New("no pod in the request"), "[filter] unable to get a Pod from the request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	for _, vol := range inputData.Pod.Spec.Volumes {
		if vol.PersistentVolumeClaim == nil {
			continue
		}

		pvc := &v1.PersistentVolumeClaim{}
		if err := s.client.Get(s.ctx, client.ObjectKey{Name: inputData.Pod.Name, Namespace: vol.PersistentVolumeClaim.ClaimName}, pvc); err != nil {
			s.log.Warning("[filter] unable to get a pvc: "+vol.PersistentVolumeClaim.ClaimName, "err: ", err.Error())
			return
		}
		if pvc.Annotations != nil && pvc.Annotations[restoreAnnotation] == "true" {
			s.log.Warning("[filter] volume restore is in progress for pvc: " + vol.PersistentVolumeClaim.ClaimName)
			return
		}

        filteredNodes := []v1.Node{}
        
	}
}
