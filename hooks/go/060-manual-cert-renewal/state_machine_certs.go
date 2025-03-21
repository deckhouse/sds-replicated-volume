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
)

const SecretExpirationThreshold = time.Hour * 24 * 30

const caExpiryDurationStr = "8760h"               // 1 year
const certExpiryDuration = (24 * time.Hour) * 365 // 1 year

const (
	SchedulerExtenderCertSecretName = "linstor-scheduler-extender-https-certs"

	ControllerHttpsCertSecretName = "linstor-controller-https-cert"
	ClientHttpsCertSecretName     = "linstor-client-https-cert"
	ControllerSslCertSecretName   = "linstor-controller-ssl-cert"
	NodeSslCertSecretName         = "linstor-node-ssl-cert"

	WebhookSchedulerAdmissionCertSecretName = "linstor-scheduler-admission-certs"
	WebhookHttpsCertSecretName              = "webhooks-https-certs"

	SpaasCertSecretName = "spaas-certs"
)

func (s *stateMachine) isExpiringAnyCerts() (bool, error) {
	var allIsExpiringChecks = []func() (bool, error){
		s.isExpiringSchedulerExtenderCerts,
		s.isExpiringLinstorCerts,
		s.isExpiringWebhookCerts,
		s.isExpiringSpaasCerts,
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

func (s *stateMachine) isExpiringSpaasCerts() (bool, error) {
	if _, ok := s.trigger.Data[TriggerKeyForce]; ok {
		return true, nil
	}

	secret, err := s.getSecret(SpaasCertSecretName, false)
	if err != nil {
		return false, err
	}
	return utils.AnyCertIsExpiringSoon(s.log, secret, CertExpirationThreshold)
}

func (s *stateMachine) isExpiringSchedulerExtenderCerts() (bool, error) {
	if _, ok := s.trigger.Data[TriggerKeyForce]; ok {
		return true, nil
	}

	secret, err := s.getSecret(SchedulerExtenderCertSecretName, false)
	if err != nil {
		return false, err
	}
	return utils.AnyCertIsExpiringSoon(s.log, secret, CertExpirationThreshold)
}

func (s *stateMachine) isExpiringCertsList(secrets []*v1.Secret) (bool, error) {
	// if any cert is expiring - renew everything, since they share same CA anyway
	var anyCertIsExpiring bool
	var errs error
	for _, secret := range secrets {
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
			s.log.Warn("there were problems during cert parsing, but renew is required anyway", "errs", errs)
		}
		return true, nil
	}
	return false, errs

}

func (s *stateMachine) isExpiringWebhookCerts() (bool, error) {
	if _, ok := s.trigger.Data[TriggerKeyForce]; ok {
		return true, nil
	}

	var schedulerAdmissionSecret,
		webhookHttpsSecret,
		err = s.getWebhookCertSecrets()
	if err != nil {
		return false, err
	}

	return s.isExpiringCertsList([]*v1.Secret{
		schedulerAdmissionSecret,
		webhookHttpsSecret,
	})
}

func (s *stateMachine) isExpiringLinstorCerts() (bool, error) {
	if _, ok := s.trigger.Data[TriggerKeyForce]; ok {
		return true, nil
	}

	var controllerHttpsSecret,
		clientHttpsSecret,
		controllerSslSecret,
		nodeSslSecret,
		err = s.getLinstorCertSecrets()
	if err != nil {
		return false, err
	}

	return s.isExpiringCertsList([]*v1.Secret{
		controllerHttpsSecret, clientHttpsSecret,
		controllerSslSecret, nodeSslSecret,
	})
}

func (s *stateMachine) getWebhookCertSecrets() (
	schedulerAdmissionSecret *v1.Secret,
	webhookHttpsSecret *v1.Secret,
	err error,
) {
	schedulerAdmissionSecret, err = s.getSecret(WebhookSchedulerAdmissionCertSecretName, false)
	if err != nil {
		return nil, nil, err
	}

	webhookHttpsSecret, err = s.getSecret(WebhookHttpsCertSecretName, false)
	if err != nil {
		return nil, nil, err
	}
	return
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

func (s *stateMachine) renewCerts() error {
	s.log.Info("renewCerts")

	if err := s.renewSchedulerExtenderCerts(); err != nil {
		return fmt.Errorf("renewSchedulerExtenderCerts: %w", err)
	}

	if err := s.renewLinstorCerts(); err != nil {
		return fmt.Errorf("renewLinstorCerts: %w", err)
	}

	if err := s.renewWebhookCerts(); err != nil {
		return fmt.Errorf("renewLinstorCerts: %w", err)
	}

	if err := s.renewSpaasCert(); err != nil {
		return fmt.Errorf("renewSpaasCert: %w", err)
	}

	return nil
}

func (s *stateMachine) renewSpaasCert() error {
	secret, err := s.getSecret(SpaasCertSecretName, false)
	if err != nil {
		return err
	}

	if expiring, err := s.isExpiringSpaasCerts(); err != nil {
		return fmt.Errorf("parsing cert %s: %w", SpaasCertSecretName, err)
	} else if !expiring {
		s.log.Debug("cert is fresh", "name", SpaasCertSecretName)
		return nil
	}

	if err := s.generateAndSaveCert(
		secret,
		SelfSignedCertValues{
			CN:           "spaas",
			CACNOverride: "spaas-ca",
			SANs: []string{
				"spaas",
				"spaas." + consts.ModuleNamespace,
				"spaas." + consts.ModuleNamespace + ".svc",
			},
			ValuesPath: consts.ModuleName + ".internal.spaasCert",
		},
	); err != nil {
		return err
	}

	return nil
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
			CN: "linstor-scheduler-extender",
			SANs: []string{
				"linstor-scheduler-extender",
				"linstor-scheduler-extender." + consts.ModuleNamespace,
				"linstor-scheduler-extender." + consts.ModuleNamespace + ".svc",
				"%CLUSTER_DOMAIN%://linstor-scheduler-extender." + consts.ModuleNamespace + ".svc",
			},
			ValuesPath: consts.ModuleName + ".internal.customSchedulerExtenderCert",
		},
	); err != nil {
		return err
	}

	return nil
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

	// renew cert 1
	if err := s.generateAndSaveCert(
		controllerHttpsSecret,
		SelfSignedCertValues{
			CA:         ca,
			CN:         "linstor-controller",
			Usages:     keyUsages,
			SANs:       sans,
			ValuesPath: consts.ModuleName + ".internal.httpsControllerCert",
		},
	); err != nil {
		return err
	}

	// renew cert 2
	if err := s.generateAndSaveCert(
		clientHttpsSecret,
		SelfSignedCertValues{
			CA:         ca,
			CN:         "linstor-client",
			Usages:     keyUsages,
			SANs:       nil,
			ValuesPath: consts.ModuleName + ".internal.httpsClientCert",
		},
	); err != nil {
		return err
	}

	// renew cert 3
	if err := s.generateAndSaveCert(
		controllerSslSecret,
		SelfSignedCertValues{
			CA:         ca,
			CN:         "linstor-controller",
			Usages:     keyUsages,
			SANs:       sans,
			ValuesPath: consts.ModuleName + ".internal.sslControllerCert",
		},
	); err != nil {
		return err
	}

	// renew cert 4
	if err := s.generateAndSaveCert(
		nodeSslSecret,
		SelfSignedCertValues{
			CA:         ca,
			CN:         "linstor-node",
			Usages:     keyUsages,
			SANs:       nil,
			ValuesPath: consts.ModuleName + ".internal.sslNodeCert",
		},
	); err != nil {
		return err
	}

	return nil
}

func (s *stateMachine) renewWebhookCerts() error {
	if expiring, err := s.isExpiringWebhookCerts(); err != nil {
		return fmt.Errorf("checking certs: %w", err)
	} else if !expiring {
		s.log.Debug("all webhook certs are fresh")
		return nil
	}

	var schedulerAdmissionSecret,
		webhookHttpsSecret,
		err = s.getWebhookCertSecrets()
	if err != nil {
		return err
	}

	// renew CA
	ca, err := certificate.GenerateCA(
		"linstor-scheduler-admission",
		certificate.WithCAExpiry(caExpiryDurationStr),
	)
	if err != nil {
		return fmt.Errorf("generate ca: %w", err)
	}

	// renew cert 1
	if err := s.generateAndSaveCert(
		schedulerAdmissionSecret,
		SelfSignedCertValues{
			CA: ca,
			CN: "linstor-scheduler-admission",
			SANs: []string{
				"linstor-scheduler-admission",
				"linstor-scheduler-admission." + consts.ModuleNamespace,
				"linstor-scheduler-admission." + consts.ModuleNamespace + ".svc",
			},
			ValuesPath: consts.ModuleName + ".internal.webhookCert",
		},
	); err != nil {
		return err
	}

	// renew cert 2
	if err := s.generateAndSaveCert(
		webhookHttpsSecret,
		SelfSignedCertValues{
			CA: ca,
			CN: "webhooks",
			SANs: []string{
				"webhooks",
				"webhooks." + consts.ModuleNamespace,
				"webhooks." + consts.ModuleNamespace + ".svc",
				"%CLUSTER_DOMAIN%://webhooks." + consts.ModuleNamespace + ".svc",
			},
			ValuesPath: consts.ModuleName + ".internal.customWebhookCert",
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

	s.log.Info("generated and saved cert", "name", secret.Name)

	return nil
}

type SelfSignedCertValues struct {
	CA *certificate.Authority
	// if CA is nil, it will be generated. Override the default CN for it, if needed
	CACNOverride string
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

		cacn := input.CN
		if input.CACNOverride != "" {
			cacn = input.CACNOverride
		}

		input.CA, err = certificate.GenerateCA(
			cacn,
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
