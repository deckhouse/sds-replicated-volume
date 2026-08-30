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
	"context"
	"fmt"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	chcrt "github.com/deckhouse/module-sdk/common-hooks/tls-certificate"
	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/certificate"
	"github.com/deckhouse/module-sdk/pkg/registry"
)

const (
	// CASecretCertKey is the key of the CA certificate in the CA secret of a cert group.
	// It ends with '.crt' on purpose: the expiring-certs hook only inspects such keys.
	CASecretCertKey = "ca.crt"
	// CASecretKeyKey is the key of the CA private key in the CA secret of a cert group.
	CASecretKeyKey = "ca.key"
)

type GenSelfSignedTLSGroupHookConf []GenSelfSignedTLSHookConf

func MustNewGenSelfSignedTLSGroupHookConf(confs ...GenSelfSignedTLSHookConf) GenSelfSignedTLSGroupHookConf {
	res, err := NewGenSelfSignedTLSGroupHookConf(confs...)
	if err != nil {
		panic(fmt.Sprintf("GenSelfSignedTLSGroupHookConf is invalid: %v", err))
	}
	return res
}

func NewGenSelfSignedTLSGroupHookConf(confs ...GenSelfSignedTLSHookConf) (GenSelfSignedTLSGroupHookConf, error) {
	var res GenSelfSignedTLSGroupHookConf

	if len(confs) == 0 {
		return res, fmt.Errorf("no configs")
	}

	for i := range confs {
		confs[i].validateAndApplyDefaults()

		if i == 0 {
			if confs[0].CommonCAValuesPath != "" {
				return res, fmt.Errorf("CommonCAValuesPath is not supported")
			}
			if confs[0].CommonCACanonicalName == "" {
				return res, fmt.Errorf("CommonCACanonicalName is required for a group of certs")
			}
			if confs[0].CASecretName == "" {
				return res, fmt.Errorf("CASecretName is required for a group of certs")
			}
			if confs[0].CAValuesPath == "" {
				return res, fmt.Errorf("CAValuesPath is required for a group of certs")
			}
		} else {
			// ensure all confs have same properties
			if confs[i].CommonCAValuesPath != confs[i-1].CommonCAValuesPath {
				return res, fmt.Errorf("group of certs should have the same CommonCAValuesPath")
			}
			if confs[i].CommonCACanonicalName != confs[i-1].CommonCACanonicalName {
				return res, fmt.Errorf("group of certs should have the same CommonCACanonicalName")
			}
			if confs[i].CAExpiryDuration != confs[i-1].CAExpiryDuration {
				return res, fmt.Errorf("group of certs should have the same CAExpiryDuration")
			}
			if confs[i].CASecretName != confs[i-1].CASecretName {
				return res, fmt.Errorf("group of certs should have the same CASecretName")
			}
			if confs[i].CAValuesPath != confs[i-1].CAValuesPath {
				return res, fmt.Errorf("group of certs should have the same CAValuesPath")
			}
		}
		res = append(res, confs[i])
	}

	return res, nil
}

func RegisterManualTLSHookEM(confs GenSelfSignedTLSGroupHookConf) bool {
	return registry.RegisterFunc(
		&pkg.HookConfig{
			OnBeforeHelm: &pkg.OrderedConfig{Order: 5},
		},
		GenManualSelfSignedTLS(confs),
	)
}

// GenManualSelfSignedTLS keeps a group of certificates, sharing a single CA, up to date.
//
// The CA (certificate and private key) is stored in its own secret, which lets leaf
// certificates be re-signed by the same CA when they are about to expire. Such a renewal is
// rolling: consumers keep trusting both the old and the new leaf certificate, so they can be
// restarted one by one.
//
// The whole group is re-issued from a brand new CA only when the CA itself is missing or is
// about to expire. That invalidates every leaf certificate of the group at once, so all
// consumers have to be restarted, which the restart-cert-consumers hook takes care of.
func GenManualSelfSignedTLS(confs GenSelfSignedTLSGroupHookConf) func(context.Context, *pkg.HookInput) error {
	return func(ctx context.Context, input *pkg.HookInput) error {
		cl, err := input.DC.GetK8sClient()
		if err != nil {
			return fmt.Errorf("getting kclient: %w", err)
		}

		r := &certGroupReconciler{
			cl:    cl,
			input: input,
			confs: confs,
			log:   input.Logger.With("caSecret", confs[0].CASecretName),
		}

		return r.reconcile(ctx)
	}
}

type certGroupReconciler struct {
	cl    client.Client
	input *pkg.HookInput
	confs GenSelfSignedTLSGroupHookConf
	log   pkg.Logger
}

func (r *certGroupReconciler) reconcile(ctx context.Context) error {
	caConf := r.confs[0]

	ca, err := r.loadCA(ctx)
	if err != nil {
		return err
	}

	leaves := make([]chcrt.CertValues, len(r.confs))
	for i, conf := range r.confs {
		if leaves[i], err = r.loadLeaf(ctx, conf); err != nil {
			return err
		}
	}

	// The CA is unusable: nothing can be signed with it, the whole group has to be re-issued.
	// The only exception is a group provisioned by a module version which did not store the CA
	// key yet: its certificates are still valid and must be preserved as is, they are re-issued
	// when the first of them approaches expiration.
	if ca == nil {
		if CertGroupIsRelevant(r.confs, leaves) {
			r.log.Info("ca key is not stored yet, keeping existing certificates until they are outdated")
			r.setLeafValues(leaves)
			return nil
		}

		return r.reissueGroup(fmt.Sprintf("ca secret %s is absent", caConf.CASecretName))
	}

	if expiring, err := certificate.IsCertificateExpiringSoon(ca.Cert, caOutdatedDuration(caConf)); err != nil {
		return r.reissueGroup(fmt.Sprintf("ca certificate cannot be parsed: %v", err))
	} else if expiring {
		return r.reissueGroup("ca certificate can no longer cover a full certificate lifespan")
	}

	r.setCAValues(ca)

	for i, conf := range r.confs {
		log := r.log.With("secretName", conf.TLSSecretName)

		if reason := CertRenewalReason(leaves[i], ca, conf, conf.SANs(r.input)); reason != "" {
			log.Info("renewing certificate with the existing ca", "reason", reason)

			cert, err := GenerateNewSelfSignedTLS(r.selfSignedCertValues(conf, ca))
			if err != nil {
				return fmt.Errorf("renewing cert %s: %w", conf.TLSSecretName, err)
			}

			leaves[i] = convCertToValues(cert)
		} else {
			log.Debug("certificate is up to date")
		}
	}

	r.setLeafValues(leaves)

	return nil
}

// reissueGroup generates a new CA and re-signs every certificate of the group with it.
func (r *certGroupReconciler) reissueGroup(reason string) error {
	r.log.Info("re-issuing the whole cert group with a new ca", "reason", reason)

	if _, err := GenerateNewSelfSignedTLSGroup(r.input, r.confs); err != nil {
		return err
	}

	return nil
}

// loadCA reads the CA of the group from its secret, falling back to the values, which hold the
// CA generated by an earlier run of this hook, not yet flushed to the secret by Helm.
// A CA which cannot be used for signing (missing, incomplete or malformed) is reported as nil.
func (r *certGroupReconciler) loadCA(ctx context.Context) (*certificate.Authority, error) {
	caConf := r.confs[0]

	secret := &v1.Secret{}
	err := r.cl.Get(
		ctx,
		types.NamespacedName{Namespace: caConf.Namespace, Name: caConf.CASecretName},
		secret,
	)
	switch {
	case err == nil:
		ca := &certificate.Authority{
			Cert: secret.Data[CASecretCertKey],
			Key:  secret.Data[CASecretKeyKey],
		}
		if len(ca.Cert) > 0 && len(ca.Key) > 0 {
			return ca, nil
		}

		r.log.Info("ca secret is incomplete")
	case client.IgnoreNotFound(err) != nil:
		return nil, fmt.Errorf("getting ca secret %s: %w", caConf.CASecretName, err)
	}

	values := r.input.Values.Get(caConf.CAPath())
	ca := &certificate.Authority{
		Cert: []byte(values.Get("crt").String()),
		Key:  []byte(values.Get("key").String()),
	}
	if len(ca.Cert) > 0 && len(ca.Key) > 0 {
		return ca, nil
	}

	return nil, nil
}

// loadLeaf reads a certificate of the group from its secret, falling back to the values for the
// same reason as loadCA does. Absent or incomplete data is reported as empty CertValues.
func (r *certGroupReconciler) loadLeaf(
	ctx context.Context,
	conf GenSelfSignedTLSHookConf,
) (chcrt.CertValues, error) {
	secret := &v1.Secret{}
	err := r.cl.Get(
		ctx,
		types.NamespacedName{Namespace: conf.Namespace, Name: conf.TLSSecretName},
		secret,
	)
	switch {
	case err == nil:
		values := chcrt.CertValues{
			CA:  string(secret.Data["ca.crt"]),
			Crt: string(secret.Data["tls.crt"]),
			Key: string(secret.Data["tls.key"]),
		}
		if isCertValuesComplete(values) {
			return values, nil
		}

		r.log.Info("secret is empty", "secretName", conf.TLSSecretName)
	case client.IgnoreNotFound(err) != nil:
		return chcrt.CertValues{}, fmt.Errorf("getting secret %s: %w", conf.TLSSecretName, err)
	}

	stored := r.input.Values.Get(conf.Path())
	values := chcrt.CertValues{
		CA:  stored.Get("ca").String(),
		Crt: stored.Get("crt").String(),
		Key: stored.Get("key").String(),
	}
	if isCertValuesComplete(values) {
		return values, nil
	}

	return chcrt.CertValues{}, nil
}

// CertRenewalReason reports why a certificate has to be re-signed by the CA of its group, or an
// empty string if it is still relevant.
func CertRenewalReason(
	values chcrt.CertValues,
	ca *certificate.Authority,
	conf GenSelfSignedTLSHookConf,
	sans []string,
) string {
	if !isCertValuesComplete(values) {
		return "certificate is absent"
	}

	if !isSamePEM(values.CA, string(ca.Cert)) {
		return "certificate is signed by another ca"
	}

	cert, err := parseCertificatePEM([]byte(values.Crt))
	if err != nil {
		return fmt.Sprintf("certificate cannot be parsed: %v", err)
	}

	if expiring, err := certificate.IsCertificateExpiringSoon([]byte(values.Crt), conf.CertOutdatedDuration); err != nil {
		return fmt.Sprintf("certificate cannot be parsed: %v", err)
	} else if expiring {
		return "certificate is expiring soon"
	}

	if missing := missingSANs(cert, sans); len(missing) > 0 {
		return fmt.Sprintf("certificate is missing SANs %s", strings.Join(missing, ", "))
	}

	return ""
}

// CertGroupIsRelevant reports whether every certificate of a group, whose CA private key is not
// stored anywhere, can be left untouched.
func CertGroupIsRelevant(confs GenSelfSignedTLSGroupHookConf, leaves []chcrt.CertValues) bool {
	for i, conf := range confs {
		if !isCertValuesComplete(leaves[i]) {
			return false
		}

		// the CA of a group provisioned by an older module version is only known through the
		// certificates themselves, so it is compared against the first of them
		if !isSamePEM(leaves[i].CA, leaves[0].CA) {
			return false
		}

		expiring, err := certificate.IsCertificateExpiringSoon([]byte(leaves[i].Crt), conf.CertOutdatedDuration)
		if err != nil || expiring {
			return false
		}

		caExpiring, err := certificate.IsCertificateExpiringSoon([]byte(leaves[i].CA), conf.CertOutdatedDuration)
		if err != nil || caExpiring {
			return false
		}
	}

	return true
}

func (r *certGroupReconciler) selfSignedCertValues(
	conf GenSelfSignedTLSHookConf,
	ca *certificate.Authority,
) SelfSignedCertValues {
	return SelfSignedCertValues{
		CA:           ca,
		CN:           conf.CN,
		CACN:         conf.CommonCACanonicalName,
		KeyAlgorithm: conf.KeyAlgorithm,
		KeySize:      conf.KeySize,
		SANs:         conf.SANs(r.input),
		Usages:       conf.UsagesStrings(),
		CAExpiry:     conf.CAExpiryDuration,
		CertExpiry:   conf.CertExpiryDuration,
	}
}

func (r *certGroupReconciler) setCAValues(ca *certificate.Authority) {
	r.input.Values.Set(r.confs[0].CAPath(), CAValues{
		Crt: string(ca.Cert),
		Key: string(ca.Key),
	})
}

func (r *certGroupReconciler) setLeafValues(leaves []chcrt.CertValues) {
	for i, conf := range r.confs {
		r.input.Values.Set(conf.Path(), leaves[i])
	}
}

// CAValues is the shape of the CA of a cert group in the module values.
type CAValues struct {
	Crt string `json:"crt"`
	Key string `json:"key"`
}

// GenerateNewSelfSignedTLSGroup issues a new CA and signs every certificate of the group with
// it, storing the CA and the certificates in the module values.
func GenerateNewSelfSignedTLSGroup(
	input *pkg.HookInput,
	confGroup GenSelfSignedTLSGroupHookConf,
) ([]*certificate.Certificate, error) {
	var res []*certificate.Certificate

	caConf := confGroup[0]

	auth, err := certificate.GenerateCA(
		caConf.CommonCACanonicalName,
		certificate.WithKeyAlgo(caConf.KeyAlgorithm),
		certificate.WithKeySize(caConf.KeySize),
		certificate.WithCAExpiry(caConf.CAExpiryDuration),
	)
	if err != nil {
		return nil, fmt.Errorf("generating ca: %w", err)
	}

	input.Values.Set(caConf.CAPath(), CAValues{
		Crt: string(auth.Cert),
		Key: string(auth.Key),
	})

	for _, conf := range confGroup {
		cert, err := GenerateNewSelfSignedTLS(
			SelfSignedCertValues{
				CA:           auth,
				CN:           conf.CN,
				CACN:         conf.CommonCACanonicalName,
				KeyAlgorithm: conf.KeyAlgorithm,
				KeySize:      conf.KeySize,
				SANs:         conf.SANs(input),
				Usages:       conf.UsagesStrings(),
				CAExpiry:     conf.CAExpiryDuration,
				CertExpiry:   conf.CertExpiryDuration,
			},
		)

		if err != nil {
			return res, fmt.Errorf("generating certs: %w", err)
		}

		res = append(res, cert)

		input.Values.Set(conf.Path(), convCertToValues(cert))
	}

	input.Logger.Info("certs initialized")

	return res, nil
}

// caOutdatedDuration is how long before its expiration the CA of a group is replaced. A CA is
// replaced as soon as it can no longer cover a full lifespan of the certificates it signs,
// otherwise a renewed certificate would outlive its own CA.
func caOutdatedDuration(caConf GenSelfSignedTLSHookConf) time.Duration {
	return caConf.CertExpiryDuration + caConf.CertOutdatedDuration
}

func isCertValuesComplete(values chcrt.CertValues) bool {
	return values.CA != "" && values.Crt != "" && values.Key != ""
}

func isSamePEM(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}
