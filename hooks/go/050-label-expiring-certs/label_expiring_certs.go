package labelexpiringcerts

import (
	"context"
	"errors"
	"fmt"
	"time"

<<<<<<< HEAD
=======
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

>>>>>>> e37389c ([internal] fixes in CI, switch to werf v2)
	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/utils"
<<<<<<< HEAD
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
=======
>>>>>>> e37389c ([internal] fixes in CI, switch to werf v2)
)

const (
	SecretCertExpire30dLabel  = "storage.deckhouse.io/sds-replicated-volume-cert-expire-in-30d"
	SecretExpirationThreshold = time.Hour * 24 * 30
)

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		Schedule: []pkg.ScheduleConfig{
			{Name: "daily", Crontab: "40 12 * * *"},
		},
		Queue: fmt.Sprintf("modules/%s", consts.ModuleName),
	},
	labelExpiringCerts,
)

func labelExpiringCerts(ctx context.Context, input *pkg.HookInput) error {
	cl := input.DC.MustGetK8sClient()

	secrets := &v1.SecretList{}
	if err := cl.List(ctx, secrets, client.InNamespace(consts.ModuleNamespace)); err != nil {
		return fmt.Errorf("listing secrets: %w", err)
	}

	var resultErr error
	for _, secret := range secrets.Items {
		log := input.Logger.With("name", secret.Name)

		if expiring, err := utils.AnyCertIsExpiringSoon(log, &secret, SecretExpirationThreshold); err != nil {
			// do not retry certificate errors, probably just a format problem
			log.Error("error checking certificates", "err", err)
			continue
		} else if !expiring {
			log.Info("no expiring certs found")

			if secret.Labels[SecretCertExpire30dLabel] == "" {
				continue
			}

			log.Info("secret have obsolete label, remove")

			delete(secret.Labels, SecretCertExpire30dLabel)
			if err := cl.Update(ctx, &secret); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("error removing label from secret: %w", err))
				log.Error("error removing label from secret", "err", err)
			}

			continue
		}

		if secret.Labels[SecretCertExpire30dLabel] != "" {
			log.Info("cert already have label, skip")
			continue
		}

		secret.Labels[SecretCertExpire30dLabel] = "true"
		if err := cl.Update(ctx, &secret); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("error adding label to secret: %w", err))
			log.Error("error adding label to secret", "err", err)
		}
	}

	return resultErr
}
