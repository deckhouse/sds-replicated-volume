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

package controller_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/oldcontroller/config"
	"github.com/deckhouse/sds-replicated-volume/images/oldcontroller/pkg/controller"
	"github.com/deckhouse/sds-replicated-volume/images/oldcontroller/pkg/logger"
)

// defaultIgnoredPrefixes mirrors the union of the system list (internal values)
// and the default user list (openapi config-values) shipped with the module.
var defaultIgnoredPrefixes = []string{
	"app.kubernetes.io/managed-by",
	"app.kubernetes.io/instance",
	"kubernetes.io/",
	"k8s.io/",
	"storage.deckhouse.io/managed-by",
	"argocd.argoproj.io/",
	"kustomize.toolkit.fluxcd.io/",
	"helm.toolkit.fluxcd.io/",
	"fleet.cattle.io/",
}

var _ = Describe("replicated-storage-class-controller label filtering", Ordered, func() {
	const (
		rscName   = "sds-replicated-volume-storage-class-label-filter"
		testZone1 = "zone-a"
		testZone2 = "zone-b"
		testZone3 = "zone-c"
	)

	var (
		ctx         = context.Background()
		cl          client.Client
		log         = logger.Logger{}
		validCFG, _ = config.NewConfig()

		validZones = []string{testZone1, testZone2, testZone3}

		rscTemplate = srv.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespaceConst,
			},
			Spec: srv.ReplicatedStorageClassSpec{
				StoragePool:   "valid",
				ReclaimPolicy: controller.ReclaimPolicyRetain,
				Replication:   controller.ReplicationConsistencyAndAvailability,
				VolumeAccess:  controller.VolumeAccessPreferablyLocal,
				Topology:      controller.TopologyTransZonal,
				Zones:         validZones,
			},
		}
	)

	BeforeAll(func() {
		cl = newFakeClientWithStatusSubresource()

		for _, zone := range validZones {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("node-%s", zone),
					Labels: map[string]string{
						controller.ZoneLabel: zone,
					},
				},
			}
			Expect(cl.Create(ctx, node)).To(Succeed())
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: validCFG.ControllerNamespace,
				Name:      validCFG.ConfigSecretName,
			},
			Data: map[string][]byte{
				"config": []byte(fmt.Sprintf(`nodeSelector:
  kubernetes.io/os: linux
storageClassLabelIgnoredPrefixes:
%s`, prefixesToYAML(defaultIgnoredPrefixes))),
			},
		}
		Expect(cl.Create(ctx, secret)).To(Succeed())
	})

	It("creates StorageClass with ignored labels dropped and managed-by enforced", func(ctx SpecContext) {
		rsc := rscTemplate
		rsc.Name = rscName
		rsc.Labels = map[string]string{
			"team":                   "storage",
			"env":                    "prod",
			"my.example.com/keep-me": "yes",

			"app.kubernetes.io/managed-by":    "helm",
			"app.kubernetes.io/instance":      "sds-replicated-volume",
			"kubernetes.io/legacy":            "true",
			"k8s.io/legacy":                   "true",
			"storage.deckhouse.io/managed-by": "someone-else",

			"argocd.argoproj.io/instance":      "infra",
			"kustomize.toolkit.fluxcd.io/name": "infra",
			"helm.toolkit.fluxcd.io/name":      "infra",
			"fleet.cattle.io/cluster":          "edge",
		}
		Expect(cl.Create(ctx, &rsc)).To(Succeed())

		request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: rsc.Namespace, Name: rsc.Name}}
		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		sc := &storagev1.StorageClass{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rscName}, sc)).To(Succeed())

		Expect(sc.Labels).To(HaveKeyWithValue(controller.ManagedLabelKey, controller.ManagedLabelValue))
		Expect(sc.Labels).To(HaveKeyWithValue("team", "storage"))
		Expect(sc.Labels).To(HaveKeyWithValue("env", "prod"))
		Expect(sc.Labels).To(HaveKeyWithValue("my.example.com/keep-me", "yes"))

		Expect(sc.Labels).NotTo(HaveKey("app.kubernetes.io/managed-by"))
		Expect(sc.Labels).NotTo(HaveKey("app.kubernetes.io/instance"))
		Expect(sc.Labels).NotTo(HaveKey("kubernetes.io/legacy"))
		Expect(sc.Labels).NotTo(HaveKey("k8s.io/legacy"))
		Expect(sc.Labels).NotTo(HaveKey("argocd.argoproj.io/instance"))
		Expect(sc.Labels).NotTo(HaveKey("kustomize.toolkit.fluxcd.io/name"))
		Expect(sc.Labels).NotTo(HaveKey("helm.toolkit.fluxcd.io/name"))
		Expect(sc.Labels).NotTo(HaveKey("fleet.cattle.io/cluster"))
		Expect(sc.Labels).To(HaveLen(4))
	})

	It("does not update StorageClass when RSC labels differ only in ignored prefixes", func(ctx SpecContext) {
		rsc := &srv.ReplicatedStorageClass{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rscName}, rsc)).To(Succeed())

		rsc.Labels["argocd.argoproj.io/instance"] = "changed"
		rsc.Labels["helm.toolkit.fluxcd.io/name"] = "changed"
		rsc.Labels["kubernetes.io/some-thing"] = "changed"
		Expect(cl.Update(ctx, rsc)).To(Succeed())

		scBefore := &storagev1.StorageClass{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rscName}, scBefore)).To(Succeed())
		rvBefore := scBefore.ResourceVersion

		request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: rsc.Namespace, Name: rsc.Name}}
		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		scAfter := &storagev1.StorageClass{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rscName}, scAfter)).To(Succeed())
		Expect(scAfter.ResourceVersion).To(Equal(rvBefore))
	})

	It("updates StorageClass when a non-ignored label changes", func(ctx SpecContext) {
		rsc := &srv.ReplicatedStorageClass{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rscName}, rsc)).To(Succeed())

		rsc.Labels["team"] = "platform"
		Expect(cl.Update(ctx, rsc)).To(Succeed())

		request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: rsc.Namespace, Name: rsc.Name}}
		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		sc := &storagev1.StorageClass{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rscName}, sc)).To(Succeed())
		Expect(sc.Labels).To(HaveKeyWithValue("team", "platform"))
		Expect(sc.Labels).To(HaveKeyWithValue(controller.ManagedLabelKey, controller.ManagedLabelValue))
		Expect(sc.Labels).NotTo(HaveKey("argocd.argoproj.io/instance"))
	})

	It("creates StorageClass with only the managed-by label when all RSC labels are ignored", func(ctx SpecContext) {
		const onlyIgnoredName = "sds-replicated-volume-only-ignored"

		rsc := rscTemplate
		rsc.Name = onlyIgnoredName
		rsc.Labels = map[string]string{
			"argocd.argoproj.io/instance": "infra",
			"helm.toolkit.fluxcd.io/name": "infra",
			"k8s.io/foo":                  "bar",
		}
		Expect(cl.Create(ctx, &rsc)).To(Succeed())

		request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: rsc.Namespace, Name: rsc.Name}}
		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, log, validCFG, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())

		sc := &storagev1.StorageClass{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: onlyIgnoredName}, sc)).To(Succeed())
		Expect(sc.Labels).To(HaveLen(1))
		Expect(sc.Labels).To(HaveKeyWithValue(controller.ManagedLabelKey, controller.ManagedLabelValue))

		Expect(cl.Delete(ctx, &rsc)).To(Succeed())
	})

	It("ignores empty-string entries inside the ignored prefixes list", func() {
		sc := controller.GenerateStorageClassFromReplicatedStorageClass(&srv.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"team": "storage",
					"env":  "prod",
				},
			},
		}, []string{"", "argocd.argoproj.io/"})

		Expect(sc.Labels).To(HaveKeyWithValue("team", "storage"))
		Expect(sc.Labels).To(HaveKeyWithValue("env", "prod"))
		Expect(sc.Labels).To(HaveKeyWithValue(controller.ManagedLabelKey, controller.ManagedLabelValue))
	})
})

func prefixesToYAML(prefixes []string) string {
	var out string
	for _, p := range prefixes {
		out += fmt.Sprintf("  - %q\n", p)
	}
	return out
}
