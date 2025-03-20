package manualcertrenewal

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deckhouse/module-sdk/pkg/certificate"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/utils"
	certificatesv1 "k8s.io/api/certificates/v1"
	v1 "k8s.io/api/core/v1"
)

const SecretExpirationThreshold = time.Hour * 24 * 30
const InternalCertNameLabelKey = "storage.deckhouse.io/internal-cert-name"

const caExpiryDurationStr = "8760h"               // 1 year
const certExpiryDuration = (24 * time.Hour) * 365 // 1 year

const (
	SchedulerExtenderCertSecretName = "linstor-scheduler-extender-https-certs"
	ControllerHttpsCertSecretName   = "linstor-controller-https-cert"
	ClientHttpsCertSecretName       = "linstor-client-https-cert"
	ControllerSslCertSecretName     = "linstor-controller-ssl-cert"
	NodeSslCertSecretName           = "linstor-node-ssl-cert"
)

const (
	SchedulerExtenderCN = "linstor-scheduler-extender"
)

func (s *stateMachine) renewCerts() error {
	s.log.Info("renewCerts")

	if err := s.renewSchedulerExtenderCerts(); err != nil {
		return fmt.Errorf("renewSchedulerExtenderCerts: %w", err)
	}

	if err := s.renewLinstorCerts(); err != nil {
		return fmt.Errorf("renewLinstorCerts: %w", err)
	}

	return nil
}

func (s *stateMachine) isExpiringSchedulerExtenderCerts() (bool, error) {
	secret, err := s.getSecret(SchedulerExtenderCertSecretName, false)
	if err != nil {
		return false, err
	}
	return utils.AnyCertIsExpiringSoon(s.log, secret, CertExpirationThreshold)
}

func (s *stateMachine) renewSchedulerExtenderCerts() error {
	secret, err := s.getSecret(SchedulerExtenderCertSecretName, false)
	if err != nil {
		return err
	}

	if expiring, err := s.isExpiringSchedulerExtenderCerts(); err != nil {
		return fmt.Errorf("parsing cert %s: %w", SchedulerExtenderCertSecretName, err)
	} else if !expiring {
		s.log.Debug("cert is fresh", "name", SchedulerExtenderCertSecretName)
		return nil
	}

	if err := s.generateAndSaveCert(
		secret,
		SelfSignedCertValues{
			CN: SchedulerExtenderCN,
			SANs: []string{
				SchedulerExtenderCN,
				strings.Join([]string{SchedulerExtenderCN, consts.ModuleNamespace}, "."),
				strings.Join([]string{SchedulerExtenderCN, consts.ModuleNamespace, "svc"}, "."),
				strings.Join([]string{"%CLUSTER_DOMAIN%://" + SchedulerExtenderCN, consts.ModuleNamespace, "svc"}, "."),
			},
		},
	); err != nil {
		return err
	}

	return nil
}

func (s *stateMachine) isExpiringAnyCerts() (bool, error) {
	var allIsExpiringChecks = []func() (bool, error){
		s.isExpiringSchedulerExtenderCerts,
		s.isExpiringLinstorCerts,
		// TODO
	}
	for _, isExpiring := range allIsExpiringChecks {
		if expiring, err := isExpiring(); err != nil {
			return false, err
		} else if expiring {
			return true, nil
		}
	}
	return false, nil
}

func (s *stateMachine) isExpiringLinstorCerts() (bool, error) {
	var controllerHttpsSecret,
		clientHttpsSecret,
		controllerSslSecret,
		nodeSslSecret,
		err = s.getLinstorCertSecrets()
	if err != nil {
		return false, err
	}

	allSecrets := []*v1.Secret{
		controllerHttpsSecret, clientHttpsSecret,
		controllerSslSecret, nodeSslSecret,
	}

	// if any cert is expiring - renew everything, since they share same CA anyway
	var anyCertIsExpiring bool
	var errs error
	for _, secret := range allSecrets {
		if expiring, err := utils.AnyCertIsExpiringSoon(
			s.log,
			secret,
			CertExpirationThreshold,
		); err != nil {
			errs = errors.Join(errs, fmt.Errorf("parsing cert %s: %w", secret.Name, err))
			continue
		} else if !expiring {
			s.log.Debug("cert is fresh", "name", secret.Name)
			continue
		}
		anyCertIsExpiring = true
	}
	if anyCertIsExpiring {
		if errs != nil {
			s.log.Warn("there were problems during cert parsing, but renew is required anyway", "errs", err)
		}
		return true, nil
	}
	return false, errs
}

func (s *stateMachine) getLinstorCertSecrets() (
	controllerHttpsSecret *v1.Secret,
	clientHttpsSecret *v1.Secret,
	controllerSslSecret *v1.Secret,
	nodeSslSecret *v1.Secret,
	err error,
) {
	controllerHttpsSecret, err = s.getSecret(ControllerHttpsCertSecretName, false)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	clientHttpsSecret, err = s.getSecret(ClientHttpsCertSecretName, false)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	controllerSslSecret, err = s.getSecret(ControllerSslCertSecretName, false)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	nodeSslSecret, err = s.getSecret(NodeSslCertSecretName, false)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return
}

func (s *stateMachine) renewLinstorCerts() error {
	if expiring, err := s.isExpiringLinstorCerts(); err != nil {
		return fmt.Errorf("checking certs: %w", err)
	} else if !expiring {
		s.log.Debug("all linstor-ca certs are fresh")
		return nil
	}

	keyUsages := []string{
		string(certificatesv1.UsageDigitalSignature),
		string(certificatesv1.UsageKeyEncipherment),
	}
	extendedKeyUsages := []string{
		string(certificatesv1.UsageServerAuth),
		string(certificatesv1.UsageClientAuth),
	}
	var _ = extendedKeyUsages // TODO

	sans := []string{
		"linstor",
		"linstor." + consts.ModuleNamespace,
		"linstor." + consts.ModuleNamespace + ".svc",
	}

	var controllerHttpsSecret,
		clientHttpsSecret,
		controllerSslSecret,
		nodeSslSecret,
		err = s.getLinstorCertSecrets()
	if err != nil {
		return err
	}

	// TODO: compare with values and change values, if needed

	// renew CA
	ca, err := certificate.GenerateCA(
		"linstor-ca",
		certificate.WithCAExpiry(caExpiryDurationStr),
	)
	if err != nil {
		return fmt.Errorf("generate ca: %w", err)
	}

	// renew certs - linstor-controller-https-cert
	if err := s.generateAndSaveCert(
		controllerHttpsSecret,
		SelfSignedCertValues{
			CA:     ca,
			CN:     "linstor-controller",
			Usages: keyUsages,
			SANs:   sans,
		},
	); err != nil {
		return err
	}

	// renew certs - linstor-client-https-cert
	if err := s.generateAndSaveCert(
		clientHttpsSecret,
		SelfSignedCertValues{
			CA:     ca,
			CN:     "linstor-client",
			Usages: keyUsages,
			SANs:   nil,
		},
	); err != nil {
		return err
	}

	// renew certs - linstor-controller-ssl-cert
	if err := s.generateAndSaveCert(
		controllerSslSecret,
		SelfSignedCertValues{
			CA:     ca,
			CN:     "linstor-controller",
			Usages: keyUsages,
			SANs:   sans,
		},
	); err != nil {
		return err
	}

	// renew certs - linstor-node-ssl-cert
	if err := s.generateAndSaveCert(
		nodeSslSecret,
		SelfSignedCertValues{
			CA:     ca,
			CN:     "linstor-node",
			Usages: keyUsages,
			SANs:   nil,
		},
	); err != nil {
		return err
	}

	return nil
}

func (s *stateMachine) generateAndSaveCert(secret *v1.Secret, values SelfSignedCertValues) error {
	cert, err := generateNewSelfSignedTLS(values)
	if err != nil {
		return fmt.Errorf("renewing cert %s: %w", secret.Name, err)
	}

	// update secrets
	if secret.Data == nil {
		secret.Data = make(map[string][]byte, 3)
	}
	secret.Data["ca.crt"] = cert.CA
	secret.Data["tls.crt"] = cert.Cert
	secret.Data["tls.key"] = cert.Key

	if err := s.cl.Update(s.ctx, secret); err != nil {
		return fmt.Errorf("updating secret %s: %w", secret.Name, err)
	}

	// set values
	type certValues struct {
		CA  string `json:"ca"`
		Crt string `json:"crt"`
		Key string `json:"key"`
	}

	s.values.Set(values.ValuesPath, certValues{
		CA:  string(cert.CA),
		Crt: string(cert.CA),
		Key: string(cert.Key),
	})

	return nil
}

type SelfSignedCertValues struct {
	CA           *certificate.Authority
	CN           string
	KeyAlgorithm string
	KeySize      int
	SANs         []string
	Usages       []string
	ValuesPath   string
}

func generateNewSelfSignedTLS(input SelfSignedCertValues) (*certificate.Certificate, error) {
	if len(input.KeyAlgorithm) == 0 {
		input.KeyAlgorithm = "ecdsa"
	}

	if input.KeySize < 128 {
		input.KeySize = 256
	}

	usages := []string{
		"signing",
		"key encipherment",
		"requestheader-client",
	}
	usages = append(usages, input.Usages...)
	input.Usages = usages

	if input.CA == nil {
		var err error

		input.CA, err = certificate.GenerateCA(
			input.CN,
			certificate.WithKeyAlgo(input.KeyAlgorithm),
			certificate.WithKeySize(input.KeySize),
			certificate.WithCAExpiry(caExpiryDurationStr))
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
		certificate.WithSigningDefaultExpiry(certExpiryDuration),
		certificate.WithSigningDefaultUsage(input.Usages),
		// TODO: support for extended_key_usages
		// func(request *csr.CertificateRequest) {
		// 	request.Extensions = append(request.Extensions, pkix.Extension{})
		// },
	)
	if err != nil {
		return nil, fmt.Errorf("generate ca: %w", err)
	}

	return cert, nil
}
