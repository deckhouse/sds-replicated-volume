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
	"strings"
	"testing"
	"time"

	chcrt "github.com/deckhouse/module-sdk/common-hooks/tls-certificate"
	"github.com/deckhouse/module-sdk/pkg/certificate"
)

const (
	testCAExpiry   = time.Hour * 24 * 365 * 10
	testCertExpiry = time.Hour * 24 * 365
	testOutdated   = time.Hour * 24 * 45
)

func testConf(sans ...string) GenSelfSignedTLSHookConf {
	conf := GenSelfSignedTLSHookConf{
		CN:                    "webhooks",
		Namespace:             "d8-sds-replicated-volume",
		TLSSecretName:         "webhooks-https-certs",
		SANs:                  chcrt.DefaultSANs(sans),
		FullValuesPathPrefix:  "sdsReplicatedVolume.internal.customWebhookCert",
		CommonCACanonicalName: "webhooks-ca",
		CASecretName:          "webhooks-ca",
		CAValuesPath:          "sdsReplicatedVolume.internal.webhooksCA",
		CAExpiryDuration:      testCAExpiry,
		CertExpiryDuration:    testCertExpiry,
		CertOutdatedDuration:  testOutdated,
	}
	conf.validateAndApplyDefaults()

	return conf
}

func mustGenerateCA(t *testing.T, expiry time.Duration) *certificate.Authority {
	t.Helper()

	ca, err := certificate.GenerateCA(
		"webhooks-ca",
		certificate.WithKeyAlgo("ecdsa"),
		certificate.WithKeySize(256),
		certificate.WithCAExpiry(expiry),
	)
	if err != nil {
		t.Fatalf("generating ca: %v", err)
	}

	return ca
}

func mustGenerateCert(
	t *testing.T,
	conf GenSelfSignedTLSHookConf,
	ca *certificate.Authority,
	sans []string,
	expiry time.Duration,
) chcrt.CertValues {
	t.Helper()

	cert, err := GenerateNewSelfSignedTLS(SelfSignedCertValues{
		CA:           ca,
		CN:           conf.CN,
		CACN:         conf.CommonCACanonicalName,
		KeyAlgorithm: conf.KeyAlgorithm,
		KeySize:      conf.KeySize,
		SANs:         sans,
		Usages:       conf.UsagesStrings(),
		CAExpiry:     conf.CAExpiryDuration,
		CertExpiry:   expiry,
	})
	if err != nil {
		t.Fatalf("generating cert: %v", err)
	}

	return convCertToValues(cert)
}

func TestCertRenewalReason(t *testing.T) {
	sans := []string{"webhooks", "webhooks.d8-sds-replicated-volume.svc", "127.0.0.1"}
	conf := testConf(sans...)
	ca := mustGenerateCA(t, testCAExpiry)

	t.Run("relevant certificate is kept", func(t *testing.T) {
		values := mustGenerateCert(t, conf, ca, sans, testCertExpiry)

		if reason := CertRenewalReason(values, ca, conf, sans); reason != "" {
			t.Fatalf("expected no renewal, got %q", reason)
		}
	})

	t.Run("absent certificate is renewed", func(t *testing.T) {
		if reason := CertRenewalReason(chcrt.CertValues{}, ca, conf, sans); reason != "certificate is absent" {
			t.Fatalf("unexpected reason %q", reason)
		}
	})

	t.Run("certificate of another ca is renewed", func(t *testing.T) {
		values := mustGenerateCert(t, conf, mustGenerateCA(t, testCAExpiry), sans, testCertExpiry)

		if reason := CertRenewalReason(values, ca, conf, sans); reason != "certificate is signed by another ca" {
			t.Fatalf("unexpected reason %q", reason)
		}
	})

	t.Run("expiring certificate is renewed", func(t *testing.T) {
		values := mustGenerateCert(t, conf, ca, sans, testOutdated/2)

		if reason := CertRenewalReason(values, ca, conf, sans); reason != "certificate is expiring soon" {
			t.Fatalf("unexpected reason %q", reason)
		}
	})

	t.Run("certificate missing a san is renewed", func(t *testing.T) {
		values := mustGenerateCert(t, conf, ca, sans, testCertExpiry)
		extended := append(append([]string{}, sans...), "webhooks.d8-sds-replicated-volume.svc.cluster.local")

		reason := CertRenewalReason(values, ca, conf, extended)
		if !strings.HasPrefix(reason, "certificate is missing SANs") {
			t.Fatalf("unexpected reason %q", reason)
		}
	})

	t.Run("unparsable certificate is renewed", func(t *testing.T) {
		values := mustGenerateCert(t, conf, ca, sans, testCertExpiry)
		values.Crt = "not a pem"

		if reason := CertRenewalReason(values, ca, conf, sans); !strings.HasPrefix(reason, "certificate cannot be parsed") {
			t.Fatalf("unexpected reason %q", reason)
		}
	})
}

func TestCertGroupIsRelevant(t *testing.T) {
	sans := []string{"webhooks"}
	conf := testConf(sans...)
	confs := GenSelfSignedTLSGroupHookConf{conf, conf}
	ca := mustGenerateCA(t, testCAExpiry)

	t.Run("group signed by one ca is kept", func(t *testing.T) {
		leaves := []chcrt.CertValues{
			mustGenerateCert(t, conf, ca, sans, testCertExpiry),
			mustGenerateCert(t, conf, ca, sans, testCertExpiry),
		}

		if !CertGroupIsRelevant(confs, leaves) {
			t.Fatal("expected the group to be kept")
		}
	})

	t.Run("group with an expiring certificate is re-issued", func(t *testing.T) {
		leaves := []chcrt.CertValues{
			mustGenerateCert(t, conf, ca, sans, testCertExpiry),
			mustGenerateCert(t, conf, ca, sans, testOutdated/2),
		}

		if CertGroupIsRelevant(confs, leaves) {
			t.Fatal("expected the group to be re-issued")
		}
	})

	t.Run("group with an expiring ca is re-issued", func(t *testing.T) {
		shortCA := mustGenerateCA(t, testOutdated/2)
		leaves := []chcrt.CertValues{
			mustGenerateCert(t, conf, shortCA, sans, testCertExpiry),
			mustGenerateCert(t, conf, shortCA, sans, testCertExpiry),
		}

		if CertGroupIsRelevant(confs, leaves) {
			t.Fatal("expected the group to be re-issued")
		}
	})

	t.Run("group split between two cas is re-issued", func(t *testing.T) {
		leaves := []chcrt.CertValues{
			mustGenerateCert(t, conf, ca, sans, testCertExpiry),
			mustGenerateCert(t, conf, mustGenerateCA(t, testCAExpiry), sans, testCertExpiry),
		}

		if CertGroupIsRelevant(confs, leaves) {
			t.Fatal("expected the group to be re-issued")
		}
	})

	t.Run("group with an absent certificate is re-issued", func(t *testing.T) {
		leaves := []chcrt.CertValues{
			mustGenerateCert(t, conf, ca, sans, testCertExpiry),
			{},
		}

		if CertGroupIsRelevant(confs, leaves) {
			t.Fatal("expected the group to be re-issued")
		}
	})
}

func TestCAIsReplacedBeforeItCannotCoverACertificate(t *testing.T) {
	conf := testConf("webhooks")

	if got, want := caOutdatedDuration(conf), testCertExpiry+testOutdated; got != want {
		t.Fatalf("ca is replaced %s before expiration, want %s", got, want)
	}

	t.Run("long living ca is kept", func(t *testing.T) {
		ca := mustGenerateCA(t, testCAExpiry)

		expiring, err := certificate.IsCertificateExpiringSoon(ca.Cert, caOutdatedDuration(conf))
		if err != nil {
			t.Fatal(err)
		}
		if expiring {
			t.Fatal("expected the ca to be kept")
		}
	})

	t.Run("ca outliving a certificate by less than its lifespan is replaced", func(t *testing.T) {
		// still valid for months, but a certificate issued now would outlive it
		ca := mustGenerateCA(t, testCertExpiry)

		expiring, err := certificate.IsCertificateExpiringSoon(ca.Cert, caOutdatedDuration(conf))
		if err != nil {
			t.Fatal(err)
		}
		if !expiring {
			t.Fatal("expected the ca to be replaced")
		}
	})
}

func TestNewGenSelfSignedTLSGroupHookConf(t *testing.T) {
	t.Run("ca secret name is required", func(t *testing.T) {
		conf := testConf("webhooks")
		conf.CASecretName = ""

		if _, err := NewGenSelfSignedTLSGroupHookConf(conf); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("ca values path is required", func(t *testing.T) {
		conf := testConf("webhooks")
		conf.CAValuesPath = ""

		if _, err := NewGenSelfSignedTLSGroupHookConf(conf); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("group members should share the ca", func(t *testing.T) {
		first := testConf("webhooks")
		second := testConf("webhooks")
		second.CASecretName = "another-ca"

		if _, err := NewGenSelfSignedTLSGroupHookConf(first, second); err == nil {
			t.Fatal("expected an error")
		}
	})
}
