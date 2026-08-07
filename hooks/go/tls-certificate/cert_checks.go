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

package tlscertificate

import (
	"crypto/x509"
	"net"
	"slices"
	"strings"

	"github.com/cloudflare/cfssl/helpers"
)

func parseCertificatePEM(pem []byte) (*x509.Certificate, error) {
	return helpers.ParseCertificatePEM(pem)
}

// missingSANs reports which of the expected SANs the certificate does not cover. SANs still
// carrying an unexpanded domain template are skipped: the domain is unknown to the hook at that
// point, so such a SAN cannot be compared.
func missingSANs(cert *x509.Certificate, expected []string) []string {
	var missing []string

	for _, san := range expected {
		if san == "" || strings.Contains(san, "://") {
			continue
		}

		if ip := net.ParseIP(san); ip != nil {
			if !slices.ContainsFunc(cert.IPAddresses, ip.Equal) {
				missing = append(missing, san)
			}

			continue
		}

		if !slices.ContainsFunc(cert.DNSNames, func(name string) bool {
			return strings.EqualFold(name, san)
		}) {
			missing = append(missing, san)
		}
	}

	return missing
}
