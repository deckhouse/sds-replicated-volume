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

package deleteobsoletecertsecrets

import (
	"context"
	"errors"
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/certs"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
)

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		OnBeforeHelm: &pkg.OrderedConfig{Order: 4},
		Queue:        fmt.Sprintf("modules/%s", consts.ModuleName),
	},
	deleteObsoleteCertSecrets,
)

// deleteObsoleteCertSecrets removes leftover certificate secrets of components which are no
// longer a part of the module. Such a secret is refreshed by nobody, so it eventually expires and
// shows up in the expiring-certificates alert with no way to renew it.
func deleteObsoleteCertSecrets(ctx context.Context, input *pkg.HookInput) error {
	cl := input.DC.MustGetK8sClient()

	var resultErr error
	for _, name := range certs.ObsoleteSecretNames {
		log := input.Logger.With("name", name)

		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: consts.ModuleNamespace},
		}

		if err := cl.Delete(ctx, secret); err != nil {
			if client.IgnoreNotFound(err) == nil {
				log.Debug("obsolete secret is already absent")
				continue
			}

			resultErr = errors.Join(resultErr, fmt.Errorf("deleting secret %s: %w", name, err))
			log.Error("error deleting obsolete secret", "err", err)

			continue
		}

		log.Info("obsolete secret deleted")
	}

	return resultErr
}
