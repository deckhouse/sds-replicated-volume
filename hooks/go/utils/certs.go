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
