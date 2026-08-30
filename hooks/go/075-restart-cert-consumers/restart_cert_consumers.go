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

package restartcertconsumers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/certs"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
)

// CertChecksumAnnotation holds the checksum of the certificates a workload consumes. On the
// workload itself it is bookkeeping only; on its pod template it is what makes Kubernetes roll
// the pods out when a certificate has been renewed.
const CertChecksumAnnotation = consts.ModuleURI + "-cert-checksum"

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		OnBeforeHelm: &pkg.OrderedConfig{Order: 6},
		Queue:        fmt.Sprintf("modules/%s", consts.ModuleName),
	},
	restartCertConsumers,
)

// restartCertConsumers restarts the workloads consuming a module certificate which has been
// renewed. Servers read their certificate once, at startup, so a renewed certificate only takes
// effect after a restart; without one the workload keeps serving the old certificate until it
// expires.
//
// Consumers are discovered by the secrets their pods reference, which covers the workloads
// rendered by the lib-helm helpers as well.
func restartCertConsumers(ctx context.Context, input *pkg.HookInput) error {
	cl := input.DC.MustGetK8sClient()

	certSecrets, err := loadCertSecrets(ctx, cl)
	if err != nil {
		return err
	}

	deployments := &appsv1.DeploymentList{}
	if err := cl.List(ctx, deployments, client.InNamespace(consts.ModuleNamespace)); err != nil {
		return fmt.Errorf("listing deployments: %w", err)
	}

	daemonSets := &appsv1.DaemonSetList{}
	if err := cl.List(ctx, daemonSets, client.InNamespace(consts.ModuleNamespace)); err != nil {
		return fmt.Errorf("listing daemonsets: %w", err)
	}

	statefulSets := &appsv1.StatefulSetList{}
	if err := cl.List(ctx, statefulSets, client.InNamespace(consts.ModuleNamespace)); err != nil {
		return fmt.Errorf("listing statefulsets: %w", err)
	}

	var resultErr error

	for i := range deployments.Items {
		w := &deployments.Items[i]
		err := reconcileWorkload(ctx, cl, input, certSecrets, w, w.Annotations, &w.Spec.Template)
		resultErr = errors.Join(resultErr, err)
	}

	for i := range daemonSets.Items {
		w := &daemonSets.Items[i]
		err := reconcileWorkload(ctx, cl, input, certSecrets, w, w.Annotations, &w.Spec.Template)
		resultErr = errors.Join(resultErr, err)
	}

	for i := range statefulSets.Items {
		w := &statefulSets.Items[i]
		err := reconcileWorkload(ctx, cl, input, certSecrets, w, w.Annotations, &w.Spec.Template)
		resultErr = errors.Join(resultErr, err)
	}

	return resultErr
}

// loadCertSecrets returns the module certificate secrets which exist in the cluster.
func loadCertSecrets(ctx context.Context, cl client.Client) (map[string]*v1.Secret, error) {
	secrets := &v1.SecretList{}
	if err := cl.List(ctx, secrets, client.InNamespace(consts.ModuleNamespace)); err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}

	own := certs.AllCertSecretNames()
	res := make(map[string]*v1.Secret, len(own))

	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if _, ok := own[secret.Name]; ok {
			res[secret.Name] = secret
		}
	}

	return res, nil
}

func reconcileWorkload(
	ctx context.Context,
	cl client.Client,
	input *pkg.HookInput,
	certSecrets map[string]*v1.Secret,
	workload client.Object,
	annotations map[string]string,
	podTemplate *v1.PodTemplateSpec,
) error {
	consumed := consumedCertSecrets(certs.AllCertSecretNames(), &podTemplate.Spec)
	if len(consumed) == 0 {
		return nil
	}

	log := input.Logger.With("name", workload.GetName(), "certSecrets", consumed)

	// A certificate secret is missing, which happens while it is being re-created. Restarting the
	// workload now would only leave its pods waiting for that secret.
	for _, name := range consumed {
		if _, ok := certSecrets[name]; !ok {
			log.Info("certificate secret is absent, waiting for it", "secretName", name)
			return nil
		}
	}

	checksum := certChecksum(certSecrets, consumed)
	known, seenBefore := annotations[CertChecksumAnnotation]

	switch {
	case known == checksum:
		log.Debug("certificates of the workload are unchanged")
		return nil
	case !seenBefore:
		// First reconciliation of this workload: the certificates it runs with are unknown, so
		// they are recorded as up to date. Restarting here would only mean a pointless rollout.
		log.Debug("recording the certificates of the workload")
	default:
		log.Info("certificates were renewed, restarting the workload")
	}

	patch, err := certChecksumPatch(checksum, seenBefore)
	if err != nil {
		return err
	}

	if err := cl.Patch(ctx, workload, client.RawPatch(types.MergePatchType, patch)); err != nil {
		log.Error("error patching workload", "err", err)
		return fmt.Errorf("patching workload %s: %w", workload.GetName(), err)
	}

	return nil
}

// consumedCertSecrets returns the names of the module certificate secrets a pod spec references,
// be it as a volume, an environment variable or a whole environment.
func consumedCertSecrets(certSecretNames map[string]struct{}, spec *v1.PodSpec) []string {
	var res []string

	add := func(name string) {
		if _, ok := certSecretNames[name]; !ok {
			return
		}
		if slices.Contains(res, name) {
			return
		}

		res = append(res, name)
	}

	for _, volume := range spec.Volumes {
		if volume.Secret != nil {
			add(volume.Secret.SecretName)
		}

		if volume.Projected == nil {
			continue
		}

		for _, source := range volume.Projected.Sources {
			if source.Secret != nil {
				add(source.Secret.Name)
			}
		}
	}

	containers := slices.Concat(spec.InitContainers, spec.Containers)
	for _, container := range containers {
		for _, env := range container.Env {
			if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
				add(env.ValueFrom.SecretKeyRef.Name)
			}
		}

		for _, envFrom := range container.EnvFrom {
			if envFrom.SecretRef != nil {
				add(envFrom.SecretRef.Name)
			}
		}
	}

	slices.Sort(res)

	return res
}

// certChecksum hashes the certificates a workload consumes. Private keys are left out on
// purpose: a key never changes without its certificate.
func certChecksum(certSecrets map[string]*v1.Secret, consumed []string) string {
	hash := sha256.New()

	for _, name := range consumed {
		secret := certSecrets[name]

		fmt.Fprintf(hash, "%s\n", name)

		for _, key := range []string{"ca.crt", "tls.crt"} {
			hash.Write(secret.Data[key])
		}
	}

	return hex.EncodeToString(hash.Sum(nil))
}

// certChecksumPatch records the checksum on the workload and, unless the workload is seen for the
// first time, on its pod template, which is what triggers the restart.
func certChecksumPatch(checksum string, restart bool) ([]byte, error) {
	annotations := map[string]string{CertChecksumAnnotation: checksum}

	patch := map[string]any{
		"metadata": map[string]any{"annotations": annotations},
	}

	if restart {
		patch["spec"] = map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{"annotations": annotations},
			},
		}
	}

	res, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshalling patch: %w", err)
	}

	return res, nil
}
