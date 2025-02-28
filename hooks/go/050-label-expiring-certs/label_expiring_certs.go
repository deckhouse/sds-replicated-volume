package labelexpiringcerts

import (
	"context"
	"errors"
	"fmt"
	"time"

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
			{Name: "daily", Crontab: "16 16 * * *"},
		},
		Queue: fmt.Sprintf("modules/%s", consts.ModuleName),
	},
	labelExpiringCerts,
)

func labelExpiringCerts(ctx context.Context, input *pkg.HookInput) error {
	cl := input.DC.MustGetK8sClient()

	var err error

	secrets := &v1.SecretList{}
	if err := cl.List(ctx, secrets, client.InNamespace(consts.ModuleNamespace)); err != nil {
		return fmt.Errorf("listing secrets: %w", err)
	}

	for _, secret := range secrets.Items {
		log := input.Logger.With("name", secret.Name)
		if secret.Labels[SecretCertExpire30dLabel] != "" {
			log.Info("cert already have label, skip")
			continue
		}

		certData := secret.Data["tls.crt"]
		if len(certData) == 0 {
			log.Info("not a certificate, skip")
			continue
		}

		if expiring, err := certificate.IsCertificateExpiringSoon(certData, SecretExpirationThreshold); err != nil {
			err = errors.Join(err, fmt.Errorf("error parsing certificate: %w", err))
			log.Error("error parsing certificate", "err", err)
			continue
		} else if !expiring {
			log.Info("certificate is OK")
			continue
		}

		secret.Labels[SecretCertExpire30dLabel] = "true"
		if err := cl.Update(ctx, &secret); err != nil {
			err = errors.Join(err, fmt.Errorf("error adding label to secret: %w", err))
			log.Error("error adding label to secret", "err", err)
		}
	}

	return err
}
