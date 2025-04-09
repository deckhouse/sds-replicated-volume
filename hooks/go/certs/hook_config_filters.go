package certs

import (
	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ignoreSuppressedEvents(cfg *pkg.HookConfig) *pkg.HookConfig {
	for _, kcfg := range cfg.Kubernetes {
		if kcfg.LabelSelector == nil {
			kcfg.LabelSelector = &v1.LabelSelector{}
		}
		kcfg.LabelSelector.MatchExpressions = append(
			kcfg.LabelSelector.MatchExpressions,
			v1.LabelSelectorRequirement{
				Key:      consts.SecretCertHookSuppressedByLabel,
				Operator: v1.LabelSelectorOpDoesNotExist,
			},
		)
	}
	return cfg
}
