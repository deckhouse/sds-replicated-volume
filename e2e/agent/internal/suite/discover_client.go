package suite

import (
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// DiscoverClient Discovers a K8s client from kubeconfig. Registers
// v1alpha1, sds-node-configurator, core, and storage schemes.
func DiscoverClient(e *etesting.E) client.Client {
	scheme := runtime.NewScheme()

	schemeFuncs := []func(s *runtime.Scheme) error{
		corev1.AddToScheme,
		storagev1.AddToScheme,
		v1alpha1.AddToScheme,
		snc.AddToScheme,
	}

	for i, f := range schemeFuncs {
		if err := f(scheme); err != nil {
			e.Fatalf("adding scheme %d: %v", i, err)
		}
	}

	kubeConfig, err := config.GetConfig()
	if err != nil {
		e.Fatalf("getting kubeconfig: %v", err)
	}

	cl, err := client.New(kubeConfig, client.Options{Scheme: scheme})
	if err != nil {
		e.Fatalf("creating client: %v", err)
	}

	return cl
}
