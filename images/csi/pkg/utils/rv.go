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

package utils

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	v1alpha2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"

	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/pkg/logger"
)

const (
	// StorageClass keys from sds-replicated-volume-controller
	StorageClassPlacementCountKey           = "replicated.csi.storage.deckhouse.io/placementCount"
	StorageClassStoragePoolKey              = "replicated.csi.storage.deckhouse.io/storagePool"
	StorageClassParamReplicasOnDifferentKey = "replicated.csi.storage.deckhouse.io/replicasOnDifferent"
	StorageClassParamReplicasOnSameKey      = "replicated.csi.storage.deckhouse.io/replicasOnSame"
	ReplicatedStorageClassParamNameKey      = "replicated.csi.storage.deckhouse.io/replicatedStorageClassName"

	ZoneLabelKey = "topology.kubernetes.io/zone"
)

// BuildReplicatedVolumeSpec composes ReplicatedVolumeSpec using StorageClass parameters and cluster resources
func BuildReplicatedVolumeSpec(
	ctx context.Context,
	cl client.Client,
	log *logger.Logger,
	scParams map[string]string,
	requestedBytes int64,
) (*v1alpha2.ReplicatedVolumeSpec, error) {
	// Size
	sizeQty := resource.NewQuantity(requestedBytes, resource.BinarySI)

	// Replicas
	var replicasByte byte = 1
	if v, ok := scParams[StorageClassPlacementCountKey]; ok && v != "" {
		switch v {
		case "1":
			replicasByte = 1
		case "2":
			replicasByte = 2
		case "3":
			replicasByte = 3
		default:
			return nil, fmt.Errorf("unsupported placementCount: %s", v)
		}
	}

	// StoragePool -> fetch ReplicatedStoragePool to get LVM info
	storagePoolName, ok := scParams[StorageClassStoragePoolKey]
	if !ok || storagePoolName == "" {
		return nil, fmt.Errorf("storage class parameter %q is required", StorageClassStoragePoolKey)
	}

	rsp := &v1alpha1.ReplicatedStoragePool{}
	if err := cl.Get(ctx, client.ObjectKey{Name: storagePoolName}, rsp); err != nil {
		return nil, fmt.Errorf("get ReplicatedStoragePool %s: %w", storagePoolName, err)
	}

	lvmSpec := v1alpha2.LVMSpec{}
	switch rsp.Spec.Type {
	case "LVMThin":
		lvmSpec.Type = "Thin"
	case "LVM":
		lvmSpec.Type = "Thick"
	default:
		return nil, fmt.Errorf("unsupported ReplicatedStoragePool type: %s", rsp.Spec.Type)
	}

	lvmSpec.LVMVolumeGroups = make([]v1alpha2.LVGRef, 0, len(rsp.Spec.LVMVolumeGroups))
	for _, g := range rsp.Spec.LVMVolumeGroups {
		ref := v1alpha2.LVGRef{Name: g.Name}
		if lvmSpec.Type == "Thin" {
			ref.ThinPoolName = g.ThinPoolName
		}
		lvmSpec.LVMVolumeGroups = append(lvmSpec.LVMVolumeGroups, ref)
	}

	// Topology and zones
	topology := "Ignored"
	zones := []string{}

	sameKey := scParams[StorageClassParamReplicasOnSameKey]
	diffKey := scParams[StorageClassParamReplicasOnDifferentKey]

	switch {
	case sameKey == ZoneLabelKey && diffKey == "kubernetes.io/hostname":
		topology = "Zonal"
	case diffKey == ZoneLabelKey:
		// TransZonal, zones must be taken from ReplicatedStorageClass
		topology = "TransZonal"
		if rscName := scParams[ReplicatedStorageClassParamNameKey]; rscName != "" {
			rsc := &v1alpha1.ReplicatedStorageClass{}
			if err := cl.Get(ctx, client.ObjectKey{Name: rscName}, rsc); err != nil {
				return nil, fmt.Errorf("get ReplicatedStorageClass %s: %w", rscName, err)
			}
			zones = append(zones, rsc.Spec.Zones...)
		} else {
			return nil, fmt.Errorf("storage class parameter %q is required for TransZonal topology", ReplicatedStorageClassParamNameKey)
		}
	default:
		topology = "Ignored"
	}

	// SharedSecret
	secret, err := generateSharedSecret()
	if err != nil {
		return nil, err
	}

	spec := &v1alpha2.ReplicatedVolumeSpec{
		Size:             *sizeQty,
		Replicas:         replicasByte,
		SharedSecret:     secret,
		LVM:              lvmSpec,
		Zones:            zones,
		Topology:         topology,
		PublishRequested: []string{},
	}
	log.Info(fmt.Sprintf("[BuildReplicatedVolumeSpec] built spec: topology=%s replicas=%d zones=%v lvmGroups=%d", topology, replicasByte, zones, len(lvmSpec.LVMVolumeGroups)))
	return spec, nil
}

// CreateReplicatedVolume creates ReplicatedVolume
func CreateReplicatedVolume(ctx context.Context, cl client.Client, name string, spec *v1alpha2.ReplicatedVolumeSpec) (*v1alpha2.ReplicatedVolume, error) {
	rv := &v1alpha2.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       *spec,
	}
	if err := cl.Create(ctx, rv); err != nil {
		return nil, err
	}
	return rv, nil
}

// GetReplicatedVolume fetches ReplicatedVolume by name
func GetReplicatedVolume(ctx context.Context, cl client.Client, name string) (*v1alpha2.ReplicatedVolume, error) {
	var rv v1alpha2.ReplicatedVolume
	if err := cl.Get(ctx, client.ObjectKey{Name: name}, &rv); err != nil {
		return nil, err
	}
	return &rv, nil
}

// DeleteReplicatedVolume deletes ReplicatedVolume
func DeleteReplicatedVolume(ctx context.Context, cl client.Client, rv *v1alpha2.ReplicatedVolume) error {
	return cl.Delete(ctx, rv)
}

// WaitForReplicatedVolumeReady waits until Ready=True with observedGeneration >= generation
func WaitForReplicatedVolumeReady(ctx context.Context, cl client.Client, name string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for ReplicatedVolume %s to become Ready", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// continue
		}
		rv, err := GetReplicatedVolume(ctx, cl, name)
		if err != nil {
			return err
		}
		if rv.Status != nil {
			cond := meta.FindStatusCondition(rv.Status.Conditions, v1alpha2.ConditionTypeReady)
			if cond != nil && cond.ObservedGeneration >= rv.Generation && cond.Status == metav1.ConditionTrue {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func generateSharedSecret() (string, error) {
	// DRBD shared-secret: up to 64 characters. Generate 32 random bytes -> base64 (approx 43 chars)
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate shared secret: %w", err)
	}
	// Use RawStdEncoding to avoid padding; ensure <=64 chars
	return base64.RawStdEncoding.EncodeToString(buf), nil
}
