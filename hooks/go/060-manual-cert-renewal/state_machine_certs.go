package manualcertrenewal

import (
	"errors"
	"fmt"
	"time"

	"github.com/deckhouse/module-sdk/pkg/certificate"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/utils"
	certificatesv1 "k8s.io/api/certificates/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const SecretExpirationThreshold = time.Hour * 24 * 30
const InternalCertNameLabelKey = "storage.deckhouse.io/internal-cert-name"

const caExpiryDurationStr = "8760h"               // 1 year
const certExpiryDuration = (24 * time.Hour) * 365 // 1 year

func (s *stateMachine) renewCerts() error {
	s.log.Info("renewCerts")

	if err := s.renewSchedulerExtenderCerts(); err != nil {
		return fmt.Errorf("renewSchedulerExtenderCerts: %w", err)
	}

	return nil
}

func (s *stateMachine) renewSchedulerExtenderCerts() error {
	name := "linstor-scheduler-extender-https-certs"

	log := s.log.With("certName", name)

	secret := &v1.Secret{}
	if err := s.cl.Get(
		s.ctx,
		types.NamespacedName{Namespace: consts.ModuleNamespace, Name: name},
		secret,
	); err != nil {
		return fmt.Errorf("getting secret %s: %w", name, err)
	}

	if expiring, err := utils.AnyCertIsExpiringSoon(log, secret.Data, CertExpirationThreshold); err != nil {
		return fmt.Errorf("parsing cert %s: %w", name, err)
	} else if !expiring {
		log.Info("cert is fresh")
		return nil
	}

	renewedCert, err := generateNewSelfSignedTLS(SelfSignedCertValues{
		CN: "linstor-scheduler-extender",
		SANs: []string{
			"linstor-scheduler-extender",
			"linstor-scheduler-extender.d8-sds-replicated-volume",
			"linstor-scheduler-extender.d8-sds-replicated-volume.svc",
			"%CLUSTER_DOMAIN%://linstor-scheduler-extender.d8-sds-replicated-volume.svc",
		},
	})
	if err != nil {
		return fmt.Errorf("renewing cert %s: %w", name, err)
	}

	secret.Data["ca.crt"] = renewedCert.CA
	secret.Data["tls.crt"] = renewedCert.Cert
	secret.Data["tls.key"] = renewedCert.Key

	if err := s.cl.Update(s.ctx, secret); err != nil {
		return fmt.Errorf("updating secret %s: %w", name, err)
	}

	// update internal values

	return nil
}

func (s *stateMachine) renewLinstorCerts() error {
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

	controllerHttpsName := "linstor-controller-https-cert"
	controllerHttpsSecret := &v1.Secret{}
	if err := s.cl.Get(
		s.ctx,
		types.NamespacedName{Namespace: consts.ModuleNamespace, Name: controllerHttpsName},
		controllerHttpsSecret,
	); err != nil {
		return fmt.Errorf("getting secret %s: %w", controllerHttpsName, err)
	}

	clientHttpsName := "linstor-client-https-cert"
	clientHttpsSecret := &v1.Secret{}
	if err := s.cl.Get(
		s.ctx,
		types.NamespacedName{Namespace: consts.ModuleNamespace, Name: clientHttpsName},
		clientHttpsSecret,
	); err != nil {
		return fmt.Errorf("getting secret %s: %w", clientHttpsName, err)
	}

	controllerSslName := "linstor-controller-ssl-cert"
	controllerSslSecret := &v1.Secret{}
	if err := s.cl.Get(
		s.ctx,
		types.NamespacedName{Namespace: consts.ModuleNamespace, Name: controllerSslName},
		controllerSslSecret,
	); err != nil {
		return fmt.Errorf("getting secret %s: %w", controllerSslName, err)
	}

	nodeSslName := "linstor-node-ssl-cert"
	nodeSslSecret := &v1.Secret{}
	if err := s.cl.Get(
		s.ctx,
		types.NamespacedName{Namespace: consts.ModuleNamespace, Name: nodeSslName},
		nodeSslSecret,
	); err != nil {
		return fmt.Errorf("getting secret %s: %w", nodeSslName, err)
	}

	allSecrets := []*v1.Secret{
		controllerHttpsSecret, clientHttpsSecret,
		controllerSslSecret, nodeSslSecret,
	}

	// if any cert is expiring - renew everything, since they share same CA anyway
	var anyCertIsExpiring bool
	var errs error
	for _, secret := range allSecrets {
		log := s.log.With("certName", secret.Name)

		if expiring, err := utils.AnyCertIsExpiringSoon(log, secret.Data, CertExpirationThreshold); err != nil {
			errs = errors.Join(errs, fmt.Errorf("parsing cert %s: %w", secret.Name, err))
			continue
		} else if !expiring {
			log.Info("cert is fresh")
			continue
		}
		anyCertIsExpiring = true
		break
	}
	if !anyCertIsExpiring {
		return errs
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
	if err := s.generateAndSaveCertToSecret(
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
	if err := s.generateAndSaveCertToSecret(
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
	if err := s.generateAndSaveCertToSecret(
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
	if err := s.generateAndSaveCertToSecret(
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

func (s *stateMachine) generateAndSaveCertToSecret(secret *v1.Secret, values SelfSignedCertValues) error {
	cert, err := generateNewSelfSignedTLS(values)
	if err != nil {
		return fmt.Errorf("renewing cert %s: %w", secret.Name, err)
	}

	// update secrets
	if err := s.saveCertToSecret(secret, cert); err != nil {
		return err
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

func (s *stateMachine) saveCertToSecret(secret *v1.Secret, cert *certificate.Certificate) error {
	if secret.Data == nil {
		secret.Data = make(map[string][]byte, 3)
	}
	secret.Data["ca.crt"] = cert.CA
	secret.Data["tls.crt"] = cert.Cert
	secret.Data["tls.key"] = cert.Key

	if err := s.cl.Update(s.ctx, secret); err != nil {
		return fmt.Errorf("updating secret %s: %w", secret.Name, err)
	}
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
