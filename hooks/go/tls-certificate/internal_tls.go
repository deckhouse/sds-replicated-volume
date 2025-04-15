/*
Copyright 2024 Flant JSC

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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudflare/cfssl/csr"
	certificatesv1 "k8s.io/api/certificates/v1"

	chcrt "github.com/deckhouse/module-sdk/common-hooks/tls-certificate"
	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/certificate"
)

const year = (24 * time.Hour) * 365

const (
	DefaultCAExpiryDuration     = year * 10 // ~10 years
	DefaultCertExpiryDuration   = year * 10 // ~10 years
	DefaultCertOutdatedDuration = year / 2  // ~6 month, just enough to renew certificate
)

type GenSelfSignedTLSHookConf struct {
	// SANs function which returns list of domain to include into cert. Use DefaultSANs helper
	SANs chcrt.SANsGenerator

	// CN - Certificate common Name
	// often it is module name
	CN string

	// Namespace - namespace for TLS secret
	Namespace string
	// TLSSecretName - TLS secret name
	// secret must be TLS secret type https://kubernetes.io/docs/concepts/configuration/secret/#tls-secrets
	// CA certificate MUST set to ca.crt key
	TLSSecretName string

	// Usages specifies valid usage contexts for keys.
	// Default: {"signing", "key encipherment"} => 5
	// See: https://tools.ietf.org/html/rfc5280#section-4.2.1.3
	//      https://tools.ietf.org/html/rfc5280#section-4.2.1.12
	// Values will be saved to KeyUsage numerical field as the following bits:
	// 	- signing => 1
	//	- digital signature => 1
	//	- content commitment => 2
	//	- key encipherment => 4
	//	- key agreement => 16
	//	- data encipherment => 8
	//	- cert sign => 32
	//	- crl sign => 64
	//	- encipher only => 128
	//	- decipher only => 256
	// Values, which will be saved to ExtKeyUsage field:
	//  - any => 0
	//  - server auth => 1
	//  - client auth => 2
	//  - code signing => 3
	//  - email protection => 4
	//  - s/mime => 4
	//  - ipsec end system => 5
	//  - ipsec tunnel => 6
	//  - ipsec user => 7
	//  - timestamping => 8
	//  - ocsp signing => 9
	//  - microsoft sgc => 10
	//  - netscape sgc => 11
	// See: github.com/cloudflare/cfssl@v1.6.5/config/config.go
	Usages []certificatesv1.KeyUsage

	// certificate encryption algorithm
	// Can be one of: "rsa", "ecdsa", "ed25519"
	// Default: "ecdsa"
	KeyAlgorithm string

	// certificate encryption algorith key size
	// The KeySize must match the KeyAlgorithm (more info: https://github.com/cloudflare/cfssl/blob/cb0a0a3b9daf7ba477e106f2f013dd68267f0190/csr/csr.go#L108)
	// Default: 256 bit
	KeySize int

	// FullValuesPathPrefix - prefix full path to store CA certificate TLS private key and cert
	// full paths will be
	//   FullValuesPathPrefix + .ca  - CA certificate
	//   FullValuesPathPrefix + .crt - TLS private key
	//   FullValuesPathPrefix + .key - TLS certificate
	// Example: FullValuesPathPrefix =  'prometheusMetricsAdapter.internal.adapter'
	// Values to store:
	// prometheusMetricsAdapter.internal.adapter.ca
	// prometheusMetricsAdapter.internal.adapter.crt
	// prometheusMetricsAdapter.internal.adapter.key
	// Data in values store as plain text
	// In helm templates you need use `b64enc` function to encode
	FullValuesPathPrefix string

	// BeforeHookCheck runs check function before hook execution. Function should return boolean 'continue' value
	// if return value is false - hook will stop its execution
	// if return value is true - hook will continue
	BeforeHookCheck func(input *pkg.HookInput) bool

	// CommonCA - full path to store CA certificate TLS private key and cert
	// full path will be
	//   CommonCAValuesPath
	// Example: CommonCAValuesPath =  'commonCaPath'
	// Values to store:
	// commonCaPath.key
	// commonCaPath.crt
	// (!) Data in values is already in base64, so
	// in helm templates you DON'T need use `b64enc` function
	CommonCAValuesPath string
	// Canonical name (CN) of common CA certificate.
	// If not specified (empty), then (if no CA cert already generated) using CN property of this struct
	CommonCACanonicalName string

	// How far in advance to renew certificates. Default is [DefaultCertOutdatedDuration].
	CertOutdatedDuration time.Duration

	// CA certificate lifespan. Default is [DefaultCAExpiryDuration].
	CAExpiryDuration time.Duration

	// Certificate lifespan. Default is [DefaultCertExpiryDuration].
	CertExpiryDuration time.Duration
}

func (conf GenSelfSignedTLSHookConf) Path() string {
	return strings.TrimSuffix(conf.FullValuesPathPrefix, ".")
}

func (conf GenSelfSignedTLSHookConf) CommonCAPath() string {
	return strings.TrimSuffix(conf.CommonCAValuesPath, ".")
}

func (conf GenSelfSignedTLSHookConf) UsagesStrings() []string {
	usageStrs := make([]string, 0, len(conf.Usages))
	for _, usage := range conf.Usages {
		usageStrs = append(usageStrs, string(usage))
	}
	return usageStrs
}

type SelfSignedCertValues struct {
	CA           *certificate.Authority
	CN           string
	CACN         string
	KeyAlgorithm string
	KeySize      int
	SANs         []string
	Usages       []string
	CAExpiry     string
	CertExpiry   time.Duration
}

func (conf *GenSelfSignedTLSHookConf) validateAndApplyDefaults() {
	if len(conf.KeyAlgorithm) == 0 {
		conf.KeyAlgorithm = "ecdsa"
	}

	if conf.KeySize < 128 {
		conf.KeySize = 256
	}

	// some fool-proof validation
	keyReq := csr.KeyRequest{A: conf.KeyAlgorithm, S: conf.KeySize}
	algo := keyReq.SigAlgo()
	if algo == x509.UnknownSignatureAlgorithm {
		panic(errors.New("unknown KeyAlgorithm"))
	}

	_, err := keyReq.Generate()
	if err != nil {
		panic(fmt.Errorf("bad KeySize/KeyAlgorithm combination: %w", err))
	}

	if conf.CertOutdatedDuration == 0 {
		conf.CertOutdatedDuration = DefaultCertOutdatedDuration
	}

	if conf.CAExpiryDuration == 0 {
		conf.CAExpiryDuration = DefaultCAExpiryDuration
	}

	if conf.CertExpiryDuration == 0 {
		conf.CertExpiryDuration = DefaultCertExpiryDuration
	}

	if conf.Usages == nil {
		conf.Usages = []certificatesv1.KeyUsage{
			certificatesv1.UsageSigning,
			certificatesv1.UsageKeyEncipherment,
		}
	}

	if len(conf.CommonCACanonicalName) == 0 {
		conf.CommonCACanonicalName = conf.CN
	}
}
func convCertToValues(cert *certificate.Certificate) chcrt.CertValues {
	return chcrt.CertValues{
		CA:  string(cert.CA),
		Crt: string(cert.Cert),
		Key: string(cert.Key),
	}
}

// GenerateNewSelfSignedTLS
//
// if you pass ca - it will be used to sign new certificate
// if pass nil ca - it will be generate to sign new certificate
func GenerateNewSelfSignedTLS(input SelfSignedCertValues) (*certificate.Certificate, error) {
	if input.CA == nil {
		var err error

		input.CA, err = certificate.GenerateCA(
			input.CACN,
			certificate.WithKeyAlgo(input.KeyAlgorithm),
			certificate.WithKeySize(input.KeySize),
			certificate.WithCAExpiry(input.CAExpiry))
		if err != nil {
			return nil, fmt.Errorf("generate ca: %w", err)
		}
	}

	cert, err := certificate.GenerateSelfSignedCert(
		input.CN,
		input.CA,
		certificate.WithSANs(input.SANs...),
		certificate.WithKeyAlgo(input.KeyAlgorithm),
		certificate.WithKeySize(input.KeySize),
		certificate.WithSigningDefaultExpiry(input.CertExpiry),
		certificate.WithSigningDefaultUsage(input.Usages),
	)
	if err != nil {
		return nil, fmt.Errorf("generate cert: %w", err)
	}

	return cert, nil
}
