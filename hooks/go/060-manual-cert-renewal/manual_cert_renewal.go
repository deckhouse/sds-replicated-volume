package manualcertrenewal

import (
	"context"
	"fmt"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"github.com/deckhouse/module-sdk/pkg/utils/ptr"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
)

const (
	PackageName              = "manualcertrenewal"
	PackageURI               = consts.ModuleURI + "-" + PackageName
	ConfigMapInProgressLabel = PackageURI + "-in-progress"
	ConfigMapCompletedLabel  = PackageURI + "-completed"
	CertRenewalTriggerName   = PackageName + "-trigger"
	snapshotName             = PackageName + "-snapshot"
	HookTimeout              = 5 * time.Minute
)

// means running locally
var devMode = os.Getenv("MANUALCERTRENEWAL_DEV_MODE") != ""

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		Kubernetes: []pkg.KubernetesConfig{
			{
				Name:                         snapshotName,
				Kind:                         "ConfigMap",
				JqFilter:                     ".",
				ExecuteHookOnSynchronization: ptr.Bool(true),
				ExecuteHookOnEvents:          ptr.Bool(true),
				NamespaceSelector: &pkg.NamespaceSelector{
					NameSelector: &pkg.NameSelector{
						MatchNames: []string{consts.ModuleNamespace},
					},
				},
				NameSelector: &pkg.NameSelector{
					MatchNames: []string{CertRenewalTriggerName},
				},
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      ConfigMapCompletedLabel,
							Operator: metav1.LabelSelectorOpDoesNotExist,
						},
						{
							Key:      ConfigMapInProgressLabel,
							Operator: metav1.LabelSelectorOpDoesNotExist,
						},
					},
				},
			},
		},
		Queue: fmt.Sprintf("modules/%s", consts.ModuleName),
	},
	manualCertRenewal,
)

func manualCertRenewal(ctx context.Context, input *pkg.HookInput) (err error) {
	defer func() {
		if r := recover(); r != nil {
			input.Logger.Error("hook panicked", "r", r, "err", err)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, hookTimeout())
	defer cancel()

	input.Logger.Debug("hook invoked")
	defer func() {
		if err != nil {
			input.Logger.Error("hook failed", "err", err)
		} else {
			input.Logger.Info("hook succeeded")
		}
	}()

	cl := mustGetClient(input)

	trigger := getTrigger(ctx, cl, input)
	if trigger == nil {
		input.Logger.Debug("trigger not found in snapshots (deleted or filtered-out), ignoring")
		return nil
	}

	s := newStateMachine(ctx, cl, input.Logger, trigger, input)

	if err := s.run(); err != nil {
		return fmt.Errorf("run: %w", err)
	}

	return nil
}

func hookTimeout() time.Duration {
	if devMode {
		return time.Hour * 24
	}
	return HookTimeout
}

func mustGetClient(input *pkg.HookInput) client.Client {
	if devMode {
		cl, err := client.New(config.GetConfigOrDie(), client.Options{})
		if err != nil {
			panic(err)
		}
		return cl
	}
	return input.DC.MustGetK8sClient()
}

func getTrigger(ctx context.Context, cl client.Client, input *pkg.HookInput) *v1.ConfigMap {
	cm := &v1.ConfigMap{}

	// use this variable for local development
	if devMode {
		err := cl.Get(
			ctx,
			types.NamespacedName{Name: CertRenewalTriggerName, Namespace: consts.ModuleNamespace},
			cm,
		)
		if err != nil {
			if errors.IsNotFound(err) {
				input.Logger.Info("trigger not found")
				return nil
			}
			panic(err)
		}
		return cm
	}

	snapshots := input.Snapshots.Get(snapshotName)

	// trigger was deleted
	if len(snapshots) == 0 {
		return nil
	}

	// below conditions should never be true, if hook is called correctly
	// anyway, we prefer not to return err to avoid repeating weird scenarios
	if len(snapshots) > 1 {
		input.Logger.Error("unexpected number of snapshots, skip", "n", len(snapshots))
		return nil
	}

	if err := snapshots[0].UnmarhalTo(cm); err != nil {
		input.Logger.Error("failed unmarshalling snapshot, skip update", "err", err)
		return nil
	}

	if _, ok := cm.Labels[ConfigMapCompletedLabel]; ok {
		input.Logger.Error("unexpected label on trigger", "labels", cm.Labels)
		return nil
	}

	return cm
}
