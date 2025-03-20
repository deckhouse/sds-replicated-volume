package manualcertrenewal

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/utils"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (s *stateMachine) getDeployment(name string, forceReload bool) (*appsv1.Deployment, error) {
	if depl, ok := s.cachedDeployments[name]; !forceReload && ok {
		return depl, nil
	}

	depl := &appsv1.Deployment{}

	if err := s.cl.Get(
		s.ctx,
		types.NamespacedName{Namespace: consts.ModuleNamespace, Name: name},
		depl,
	); err != nil {
		return nil, fmt.Errorf("getting deployment %s: %w", name, err)
	}

	utils.MapEnsureAndSet(&s.cachedDeployments, name, depl)

	return depl, nil
}

func (s *stateMachine) getDaemonSet(name string, forceReload bool) (*appsv1.DaemonSet, error) {
	if ds, ok := s.cachedDaemonSets[name]; !forceReload && ok {
		return ds, nil
	}

	ds := &appsv1.DaemonSet{}

	if err := s.cl.Get(
		s.ctx,
		types.NamespacedName{Namespace: consts.ModuleNamespace, Name: name},
		ds,
	); err != nil {
		return nil, fmt.Errorf("getting daemonset %s: %w", name, err)
	}

	utils.MapEnsureAndSet(&s.cachedDaemonSets, name, ds)

	return ds, nil
}

func (s *stateMachine) getSecret(name string, forceReload bool) (*v1.Secret, error) {
	if secret, ok := s.cachedSecrets[name]; !forceReload && ok {
		return secret, nil
	}

	secret := &v1.Secret{}

	if err := s.cl.Get(
		s.ctx,
		types.NamespacedName{Namespace: consts.ModuleNamespace, Name: name},
		secret,
	); err != nil {
		return nil, fmt.Errorf("getting secret %s: %w", name, err)
	}

	utils.MapEnsureAndSet(&s.cachedSecrets, name, secret)

	return secret, nil
}
