package manualcertrenewal

import (
	"fmt"
	"iter"

	"github.com/deckhouse/module-sdk/pkg/certificate"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/certs"
	tlsc "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
)

func allCertsConfigs() iter.Seq[tlsc.GenSelfSignedTLSHookConf] {
	return func(yield func(tlsc.GenSelfSignedTLSHookConf) bool) {
		for cfg := range certs.LinstorCertConfigs() {
			if !yield(cfg) {
				return
			}
		}
		for cfg := range certs.WebhookCertConfigs() {
			if !yield(cfg) {
				return
			}
		}

		if !yield(certs.SchedulerExtenderCertConfig) {
			return
		}
		if !yield(certs.SpaasCertConfig) {
			return
		}
	}
}

func (s *stateMachine) renewCerts() error {
	for conf := range allCertsConfigs() {
		if err := s.renewCert(conf); err != nil {
			return err
		}
	}

	return nil
}

func (s *stateMachine) renewCert(conf tlsc.GenSelfSignedTLSHookConf) error {
	secret, err := s.getSecret(conf.TLSSecretName, false)
	if err != nil {
		return err
	}

	s.log.Info("renewing cert", "name", conf.TLSSecretName)

	cert := &certificate.Certificate{
		Key:  secret.Data["tls.key"],
		Cert: secret.Data["tls.crt"],
		CA:   secret.Data["ca.crt"],
	}

	_, err = tlsc.GenerateSelfSignedTLSIfNeeded(
		conf,
		s.hookInput,
		cert,
		true,
	)
	if err != nil {
		return err
	}

	// s.hookInput.Values & cert were modified, update secret
	if secret.Data == nil {
		secret.Data = make(map[string][]byte, 3)
	}
	secret.Data["tls.key"] = cert.Key
	secret.Data["tls.crt"] = cert.Cert
	secret.Data["ca.crt"] = cert.CA

	if err := s.cl.Update(s.ctx, secret); err != nil {
		return fmt.Errorf("updating secret %s: %w", secret.Name, err)
	}
	s.log.Info("generated and saved cert", "name", secret.Name)

	return nil
}
