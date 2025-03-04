package labelexpiringcerts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deckhouse/deckhouse/pkg/log"
	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/certificate"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

		if expiring, err := anyCertIsExpiringSoon(log, secret.Data); err != nil {
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

func anyCertIsExpiringSoon(log *log.Logger, data map[string][]byte) (bool, error) {
	var resultErr error

	for key, val := range data {
		keyLog := log.With("key", key)

		if !strings.HasSuffix(strings.ToLower(key), ".crt") {
			keyLog.Info("not a certificate, skip")
			continue
		}

		if len(val) == 0 {
			keyLog.Info("empty certificate, skip")
			continue
		}

		if expiring, err := certificate.IsCertificateExpiringSoon(val, SecretExpirationThreshold); err != nil {
			keyLog.Error("error parsing certificate", "err", err)
			resultErr = errors.Join(resultErr, fmt.Errorf("error parsing certificate: %w", err))
		} else if expiring {
			// drop errors as not relevant anymore
			return true, nil
		}
	}

	return false, resultErr
}
