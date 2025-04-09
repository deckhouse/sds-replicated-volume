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
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/cloudflare/cfssl/config"
	"github.com/cloudflare/cfssl/csr"
	"github.com/deckhouse/deckhouse/pkg/log"
	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/certificate"
	objectpatch "github.com/deckhouse/module-sdk/pkg/object-patch"
	"github.com/deckhouse/module-sdk/pkg/registry"
	certificatesv1 "k8s.io/api/certificates/v1"
)

const year = (24 * time.Hour) * 365

const (
	DefaultCAExpiryDuration     = year * 10 // ~10 years
	DefaultCertExpiryDuration   = year * 10 // ~10 years
	DefaultCertOutdatedDuration = year / 2  // ~6 month, just enough to renew certificate

	InternalTLSSnapshotKey = "secret"
)

type GenSelfSignedTLSHookConf struct {
	// SANs function which returns list of domain to include into cert. Use DefaultSANs helper
	SANs SANsGenerator

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

// SANsGenerator function for generating sans
type SANsGenerator func(input *pkg.HookInput) []string

var JQFilterTLS = `{
    "key": .data."tls.key",
    "crt": .data."tls.crt",
    "ca": .data."ca.crt"
}`

// RegisterInternalTLSHookEM must be used for external modules
//
// Register hook which save tls cert in values from secret.
// If secret is not created hook generate CA with long expired time
// and generate tls cert for passed domains signed with generated CA.
// That CA cert and TLS cert and private key MUST save in secret with helm.
// Otherwise, every d8 restart will generate new tls cert.
// Tls cert also has long expired time same as CA 87600h == 10 years.
// Therese tls cert often use for in cluster https communication
// with service which order tls
// Clients need to use CA cert for verify connection
func RegisterInternalTLSHookEM(conf GenSelfSignedTLSHookConf) bool {
	return registry.RegisterFunc(GenSelfSignedTLSConfig(conf), GenSelfSignedTLS(conf))
}

func GenSelfSignedTLSConfig(conf GenSelfSignedTLSHookConf) *pkg.HookConfig {
	return &pkg.HookConfig{
		OnBeforeHelm: &pkg.OrderedConfig{Order: 5},
		Kubernetes: []pkg.KubernetesConfig{
			{
				Name:       InternalTLSSnapshotKey,
				APIVersion: "v1",
				Kind:       "Secret",
				NamespaceSelector: &pkg.NamespaceSelector{
					NameSelector: &pkg.NameSelector{
						MatchNames: []string{conf.Namespace},
					},
				},
				NameSelector: &pkg.NameSelector{
					MatchNames: []string{conf.TLSSecretName},
				},
				JqFilter: JQFilterTLS,
			},
		},
		Schedule: []pkg.ScheduleConfig{
			{
				Name:    "internalTLSSchedule",
				Crontab: "42 4 * * *",
			},
		},
	}
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

func GenSelfSignedTLS(conf GenSelfSignedTLSHookConf) func(ctx context.Context, input *pkg.HookInput) error {
	return func(_ context.Context, input *pkg.HookInput) error {
		if conf.BeforeHookCheck != nil {
			passed := conf.BeforeHookCheck(input)
			if !passed {
				return nil
			}
		}

		cert := &certificate.Certificate{}

		certs, err := objectpatch.UnmarshalToStruct[certificate.Certificate](input.Snapshots, InternalTLSSnapshotKey)
		if err != nil {
			return fmt.Errorf("unmarshal to struct: %w", err)
		}

		if len(certs) > 0 {
			cert = &certs[0]
		}

		if _, err := GenerateSelfSignedTLSIfNeeded(conf, input, cert, false); err != nil {
			return err
		}

		return nil
	}
}

// New self-signed certificate will be generated when any of below is true:
//   - len(currentCert.Cert) == 0
//   - conf.CommonCAValuesPath is not empty and it's CA is not found in values,
//     outdated, or doesn't match currentCert.CA
//   - currentCert.Cert is outdated, or doesn't match important values from
//     conf - "irrelevant"
//   - forceGenerate is true
//
// Generated certificate will be written to currentCert, set to
// input.Values, and true will be returned.
func GenerateSelfSignedTLSIfNeeded(
	conf GenSelfSignedTLSHookConf,
	input *pkg.HookInput,
	currentCert *certificate.Certificate,
	forceGenerate bool,
) (bool, error) {
	conf.validateAndApplyDefaults()

	var auth *certificate.Authority
	var err error

	mustGenerate := forceGenerate

	useCommonCA := conf.CommonCAValuesPath != ""

	// 1) get and validate common ca
	// 2) if not valid:
	// 2.1) regenerate common ca
	// 2.2) save new common ca in values
	// 2.3) mark certificates to regenerate
	if useCommonCA {
		auth, err = getCommonCA(input, conf)
		if err != nil {
			input.Logger.Info("getCommonCA error", log.Err(err))

			auth, err = certificate.GenerateCA(
				conf.CommonCACanonicalName,
				certificate.WithKeyAlgo(conf.KeyAlgorithm),
				certificate.WithKeySize(conf.KeySize),
				certificate.WithCAExpiry(conf.CAExpiryDuration.String()))
			if err != nil {
				return false, fmt.Errorf("generate ca: %w", err)
			}

			input.Values.Set(conf.CommonCAPath(), auth)

			mustGenerate = true
		}
	}

	// if no certificate - regenerate
	if len(currentCert.Cert) == 0 {
		mustGenerate = true
	} else {
		// update certificate if less than 6 month left. We create certificate for 10 years, so it looks acceptable
		// and we don't need to create Crontab schedule
		caOutdated, err := isOutdatedCA(currentCert.CA, conf.CertOutdatedDuration)
		if err != nil {
			input.Logger.Error("is outdated ca", log.Err(err))
		}

		// if common ca and cert ca are not equal - regenerate cert
		if useCommonCA && !slices.Equal(auth.Cert, currentCert.CA) {
			input.Logger.Info("common ca is not equal cert ca")

			caOutdated = true
		}

		certOutdatedOrIrrelevant, err := isIrrelevantCert(currentCert.Cert, input, conf)
		if err != nil {
			input.Logger.Error("is irrelevant cert", log.Err(err))
		}

		// In case of errors, both these flags are false to avoid regeneration loop for the
		// certificate.
		mustGenerate = mustGenerate || caOutdated || certOutdatedOrIrrelevant
	}

	if mustGenerate {
		var newCert *certificate.Certificate
		if newCert, err = GenerateNewSelfSignedTLS(
			SelfSignedCertValues{
				CA:           auth,
				CN:           conf.CN,
				CACN:         conf.CommonCACanonicalName,
				KeyAlgorithm: conf.KeyAlgorithm,
				KeySize:      conf.KeySize,
				SANs:         conf.SANs(input),
				Usages:       conf.UsagesStrings(),
				CAExpiry:     conf.CAExpiryDuration.String(),
				CertExpiry:   conf.CertExpiryDuration,
			},
		); err != nil {
			return false, fmt.Errorf("generate new self signed tls: %w", err)
		}
		*currentCert = *newCert
	}

	input.Values.Set(conf.Path(), convCertToValues(currentCert))
	return mustGenerate, nil
}

type CertValues struct {
	CA  string `json:"ca"`
	Crt string `json:"crt"`
	Key string `json:"key"`
}

// The certificate mapping "cert" -> "crt". We are migrating to "crt" naming for certificates
// in values.
func convCertToValues(cert *certificate.Certificate) CertValues {
	return CertValues{
		CA:  string(cert.CA),
		Crt: string(cert.Cert),
		Key: string(cert.Key),
	}
}

var ErrCertificateIsNotFound = errors.New("certificate is not found")
var ErrCAIsInvalidOrOutdated = errors.New("ca is invalid or outdated")

func getCommonCA(input *pkg.HookInput, conf GenSelfSignedTLSHookConf) (*certificate.Authority, error) {
	auth := new(certificate.Authority)

	ca, ok := input.Values.GetOk(conf.CommonCAPath())
	if !ok {
		return nil, ErrCertificateIsNotFound
	}

	err := json.Unmarshal([]byte(ca.String()), auth)
	if err != nil {
		return nil, err
	}

	if len(auth.Cert) == 0 {
		input.Logger.Info("empty auth.Cert", "ca", ca, "caString", ca.String(), "path", conf.CommonCAPath())
	}

	outdated, err := isOutdatedCA(auth.Cert, conf.CertOutdatedDuration)
	if err != nil {
		input.Logger.Error("is outdated ca", log.Err(err))
		return nil, err
	}

	if !outdated {
		return auth, nil
	}

	return nil, ErrCAIsInvalidOrOutdated
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
		return nil, fmt.Errorf("generate ca: %w", err)
	}

	return cert, nil
}

// check certificate duration and SANs list
func isIrrelevantCert(
	certData []byte,
	input *pkg.HookInput,
	conf GenSelfSignedTLSHookConf,
) (bool, error) {
	cert, err := certificate.ParseCertificate(certData)
	if err != nil {
		return false, fmt.Errorf("parse certificate: %w", err)
	}

	// expiration
	if time.Until(cert.NotAfter) < conf.CertOutdatedDuration {
		input.Logger.Info("cert not relevant: expiring soon",
			"expectedAtLeast", conf.CertOutdatedDuration,
			"got", time.Until(cert.NotAfter),
		)
		return true, nil
	}

	// SANs = cert.DNSNames + cert.IPAddresses
	var dnsNames []string
	var ipAddrs []net.IP
	for _, san := range conf.SANs(input) {
		if ip := net.ParseIP(san); ip != nil {
			ipAddrs = append(ipAddrs, ip)
		} else {
			dnsNames = append(dnsNames, san)
		}
	}

	// DNSNames
	slices.Sort(dnsNames)
	slices.Sort(cert.DNSNames)
	if !slices.Equal(dnsNames, cert.DNSNames) {
		input.Logger.Info("cert not relevant: DNS name mismatch",
			"expected", dnsNames,
			"got", cert.DNSNames,
		)
		return true, nil
	}

	// IPAddresses
	ipCompare := func(a, b net.IP) int { return strings.Compare(a.String(), b.String()) }
	ipEq := func(a, b net.IP) bool { return net.IP.Equal(a, b) }
	slices.SortFunc(ipAddrs, ipCompare)
	slices.SortFunc(cert.IPAddresses, ipCompare)
	if !slices.EqualFunc(ipAddrs, cert.IPAddresses, ipEq) {
		input.Logger.Info("cert not relevant: IPs mismatch",
			"expected", ipAddrs,
			"got", cert.IPAddresses,
		)
		return true, nil
	}

	// KeyUsages = cert.KeyUsage + cert.ExtKeyUsage
	var expectedKeyUsage x509.KeyUsage
	var expectedExtKeyUsages []x509.ExtKeyUsage

	for _, kuStr := range conf.UsagesStrings() {
		if ku, ok := config.KeyUsage[kuStr]; ok {
			expectedKeyUsage |= ku
		} else if eku, ok := config.ExtKeyUsage[kuStr]; ok {
			expectedExtKeyUsages = append(expectedExtKeyUsages, eku)
		}
	}

	if expectedKeyUsage != cert.KeyUsage {
		input.Logger.Info("cert not relevant: KeyUsage mismatch",
			"expected", expectedKeyUsage,
			"got", cert.KeyUsage,
		)
		return true, nil
	}

	slices.Sort(cert.ExtKeyUsage)
	slices.Sort(expectedExtKeyUsages)
	if !slices.Equal(expectedExtKeyUsages, cert.ExtKeyUsage) {
		input.Logger.Info("cert not relevant: ExtKeyUsage mismatch",
			"expected", expectedExtKeyUsages,
			"got", cert.ExtKeyUsage,
		)
		return true, nil
	}

	return false, nil
}

func isOutdatedCA(ca []byte, certOutdatedDuration time.Duration) (bool, error) {
	// Issue a new certificate if there is no CA in the secret.
	// Without CA it is not possible to validate the certificate.
	if len(ca) == 0 {
		return true, nil
	}

	cert, err := certificate.ParseCertificate(ca)
	if err != nil {
		return false, fmt.Errorf("parse certificate: %w", err)
	}

	if time.Until(cert.NotAfter) < certOutdatedDuration {
		return true, nil
	}

	return false, nil
}

// DefaultSANs helper to generate list of sans for certificate
// you can also use helpers:
//
//	ClusterDomainSAN(value) to generate sans with respect of cluster domain (e.g.: "app.default.svc" with "cluster.local" value will give: app.default.svc.cluster.local
//	PublicDomainSAN(value)
func DefaultSANs(sans []string) SANsGenerator {
	return func(input *pkg.HookInput) []string {
		res := make([]string, 0, len(sans))

		clusterDomain := input.Values.Get("global.discovery.clusterDomain").String()
		publicDomainTemplate := input.Values.Get("global.modules.publicDomainTemplate").String()

		for _, san := range sans {
			switch {
			case strings.HasPrefix(san, publicDomainPrefix) && publicDomainTemplate != "":
				san = getPublicDomainSAN(publicDomainTemplate, san)

			case strings.HasPrefix(san, clusterDomainPrefix) && clusterDomain != "":
				san = getClusterDomainSAN(clusterDomain, san)
			}

			res = append(res, san)
		}

		return res
	}
}
