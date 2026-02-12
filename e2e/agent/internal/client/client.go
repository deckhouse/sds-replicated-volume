package client

import (
	"fmt"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

func NewScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	var schemeFuncs = []func(s *runtime.Scheme) error{
		corev1.AddToScheme,
		storagev1.AddToScheme,
		v1alpha1.AddToScheme,
		snc.AddToScheme,
	}

	for i, f := range schemeFuncs {
		if err := f(scheme); err != nil {
			return nil, fmt.Errorf("adding scheme %d: %w", i, err)
		}
	}

	return scheme, nil
}

func NewClient() (client.Client, error) {
	kubeConfig, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("getting config: %w", err)
	}

	scheme, err := NewScheme()
	if err != nil {
		return nil, fmt.Errorf("scheme: %w", err)
	}

	clientOpts := client.Options{
		Scheme: scheme,
	}

	return client.New(kubeConfig, clientOpts)
}
