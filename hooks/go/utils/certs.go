package utils

import (
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/certificate"
)

func AnyCertIsExpiringSoon(
	log pkg.Logger,
	secret *v1.Secret,
	durationLeft time.Duration,
) (bool, error) {
	var resultErr error

	log = log.With("name", secret.Name)

	for key, val := range secret.Data {
		keyLog := log.With("key", key)

		if !strings.HasSuffix(strings.ToLower(key), ".crt") {
			keyLog.Debug("not a certificate, skip")
			continue
		}

		if len(val) == 0 {
			keyLog.Debug("empty certificate, skip")
			continue
		}

		if expiring, err := certificate.IsCertificateExpiringSoon(val, durationLeft); err != nil {
			keyLog.Warn("error parsing certificate", "err", err)
			resultErr = errors.Join(resultErr, fmt.Errorf("error parsing certificate: %w", err))
		} else if expiring {
			// drop errors as not relevant anymore
			return true, nil
		}
	}

	return false, resultErr
}
