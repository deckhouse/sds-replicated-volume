package utils

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/certificate"
)

func AnyCertIsExpiringSoon(
	log pkg.Logger,
	data map[string][]byte,
	durationLeft time.Duration,
) (bool, error) {
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

		if expiring, err := certificate.IsCertificateExpiringSoon(val, durationLeft); err != nil {
			keyLog.Error("error parsing certificate", "err", err)
			resultErr = errors.Join(resultErr, fmt.Errorf("error parsing certificate: %w", err))
		} else if expiring {
			// drop errors as not relevant anymore
			return true, nil
		}
	}

	return false, resultErr
}
