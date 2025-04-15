package manualcertrenewal

import (
	"fmt"
	"iter"

	"github.com/deckhouse/sds-replicated-volume/hooks/go/certs"
	tlsc "github.com/deckhouse/sds-replicated-volume/hooks/go/tls-certificate"
)

var allCertGroupsConfigs = makeAllCertGroupsConfigs()

func makeAllCertGroupsConfigs() iter.Seq[tlsc.GenSelfSignedTLSGroupHookConf] {
	return func(yield func(tlsc.GenSelfSignedTLSGroupHookConf) bool) {
		if !yield(certs.LinstorCertConfigs()) {
			return
		}
		if !yield(certs.WebhookCertConfigs()) {
			return
		}
		if !yield(certs.SchedulerExtenderCertConfig) {
			return
		}
		if !yield(certs.SpaasCertConfig) {
			return
		}
	}
}

func allCertConfigs() iter.Seq[tlsc.GenSelfSignedTLSHookConf] {
	return func(yield func(tlsc.GenSelfSignedTLSHookConf) bool) {
		for confs := range allCertGroupsConfigs {
			for _, conf := range confs {
				if !yield(conf) {
					return
				}
			}
		}
	}
}

func (s *stateMachine) renewCerts() error {
	for groupConf := range allCertGroupsConfigs {
		if err := s.renewCertGroup(groupConf); err != nil {
			return err
		}
	}

	return nil
}

func (s *stateMachine) renewCertGroup(confs tlsc.GenSelfSignedTLSGroupHookConf) error {
	newCerts, err := tlsc.GenerateNewSelfSignedTLSGroup( // input.Values will be set there
		s.hookInput,
		confs,
	)
	if err != nil {
		return fmt.Errorf("generating new cert group: %w", err)
	}

	for i, cert := range newCerts {
		secret, err := s.getSecret(confs[i].TLSSecretName, false)
		if err != nil {
			return err
		}
		secret.Data["tls.key"] = cert.Key
		secret.Data["tls.crt"] = cert.Cert
		secret.Data["ca.crt"] = cert.CA
		if err := s.cl.Update(s.ctx, secret); err != nil {
			return fmt.Errorf("updating secret %s: %w", secret.Name, err)
		}
		s.log.Info("generated and saved cert", "name", secret.Name)
	}

	return nil
}
