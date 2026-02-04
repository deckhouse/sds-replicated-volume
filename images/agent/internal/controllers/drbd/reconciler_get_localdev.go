//go:build localdev

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

package drbd

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// getCurrentNodeDRBDR gets the DRBDResource if it belongs to the current node.
func (r *Reconciler) getCurrentNodeDRBDR(
	ctx context.Context,
	req reconcile.Request,
) (*v1alpha1.DRBDResource, bool, error) {
	sf := flow.BeginStep(ctx, "get-drbdr")
	log := sf.Log()

	dr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, req.NamespacedName, dr); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, false, flow.Wrapf(err, "getting DRBDResource")
		}
		log.V(1).Info("DRBDResource not found, skipping")
		return nil, false, nil
	}

	if dr.Spec.NodeName != r.nodeName {
		log.V(1).Info("DRBDResource belongs to different node, skipping", "nodeName", dr.Spec.NodeName)
		return nil, false, nil
	}

	return dr, true, nil
}

// getRequiredCurrentNode returns a stub Node for local development.
func (r *Reconciler) getRequiredCurrentNode(ctx context.Context) (*corev1.Node, error) {
	log := log.FromContext(ctx)
	log.V(1).Info("[localdev] returning stub Node", "nodeName", r.nodeName)
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: r.nodeName,
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{
					Type:    corev1.NodeInternalIP,
					Address: "127.0.0.1",
				},
			},
		},
	}, nil
}

// getLVMLogicalVolume returns a stub LVMLogicalVolume for local development.
// Returns (nil, nil) if name is empty.
func (r *Reconciler) getLVMLogicalVolume(ctx context.Context, name string) (*snc.LVMLogicalVolume, error) {
	if name == "" {
		return nil, nil
	}
	log := log.FromContext(ctx)
	log.V(1).Info("[localdev] returning stub LVMLogicalVolume", "name", name)
	return &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: snc.LVMLogicalVolumeSpec{
			LVMVolumeGroupName:    "stub-lvg",
			ActualLVNameOnTheNode: name,
		},
	}, nil
}

// getLVMVolumeGroup returns a stub LVMVolumeGroup for local development.
// Returns (nil, nil) if name is empty.
func (r *Reconciler) getLVMVolumeGroup(ctx context.Context, name string) (*snc.LVMVolumeGroup, error) {
	if name == "" {
		return nil, nil
	}
	log := log.FromContext(ctx)
	log.V(1).Info("[localdev] returning stub LVMVolumeGroup", "name", name)
	return &snc.LVMVolumeGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: snc.LVMVolumeGroupSpec{
			ActualVGNameOnTheNode: name,
		},
	}, nil
}

// getLVGsOnNode returns a stub list with one LVMVolumeGroup for local development.
func (r *Reconciler) getLVGsOnNode(ctx context.Context) ([]snc.LVMVolumeGroup, error) {
	log := log.FromContext(ctx)
	log.V(1).Info("[localdev] returning stub LVMVolumeGroups")
	return []snc.LVMVolumeGroup{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "stub-lvg",
			},
			Spec: snc.LVMVolumeGroupSpec{
				ActualVGNameOnTheNode: "stub-vg",
			},
		},
	}, nil
}

// getLLVsForLVG returns a stub empty list for local development.
func (r *Reconciler) getLLVsForLVG(ctx context.Context, lvgName string) ([]snc.LVMLogicalVolume, error) {
	log := log.FromContext(ctx)
	log.V(1).Info("[localdev] returning stub LVMLogicalVolumes for LVG", "lvgName", lvgName)
	return []snc.LVMLogicalVolume{}, nil
}

// getDRBDRsOnNode lists all DRBDResource objects on this node using the field index.
// Returns empty slice if none found. Order is unspecified.
func (r *Reconciler) getDRBDRsOnNode(ctx context.Context) ([]v1alpha1.DRBDResource, error) {
	list := &v1alpha1.DRBDResourceList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldDRBDRByNodeName: r.nodeName,
	}); err != nil {
		return nil, err
	}
	return list.Items, nil
}
