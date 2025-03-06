package manualcertrenewal

import (
	"context"
	"fmt"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"github.com/deckhouse/module-sdk/pkg/utils/ptr"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
)

const (
	ConfigMapManualCertRenewalTrigger = "storage.deckhouse.io/sds-replicated-volume-trigger-cert-renewal"
	snapshotName                      = "manualCertRenewal"
)

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		Kubernetes: []pkg.KubernetesConfig{
			{
				Name:                         snapshotName,
				Kind:                         "ConfigMap",
				JqFilter:                     ".metadata.labels",
				ExecuteHookOnEvents:          ptr.Bool(false),
				ExecuteHookOnSynchronization: ptr.Bool(false),
				NamespaceSelector: &pkg.NamespaceSelector{
					NameSelector: &pkg.NameSelector{
						MatchNames: []string{consts.ModuleNamespace},
					},
				},
				NameSelector: &pkg.NameSelector{
					MatchNames: []string{"cert-renewal-trigger"},
				},
				// LabelSelector: &metav1.LabelSelector{
				// 	MatchExpressions: []metav1.LabelSelectorRequirement{
				// 		{
				// 			Key:      ConfigMapManualCertRenewalTrigger,
				// 			Operator: metav1.LabelSelectorOpExists,
				// 		},
				// 	},
				// },
			},
		},
		Queue: fmt.Sprintf("modules/%s", consts.ModuleName),
	},
	manualCertRenewal,
)

func manualCertRenewal(ctx context.Context, input *pkg.HookInput) error {
	// cl := input.DC.MustGetK8sClient()

	snapshots := input.Snapshots.Get(snapshotName)

	fmt.Printf("Snapshots: %d\n", len(snapshots))
	input.Logger.Info("I see 'n' snapshots", "n", len(snapshots))
	for _, s := range snapshots {
		input.Logger.Info("here it is", "s", s.String())

	}

	return nil
}
