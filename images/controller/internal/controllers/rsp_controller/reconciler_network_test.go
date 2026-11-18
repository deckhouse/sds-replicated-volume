/*
Copyright 2026 Flant JSC

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

package rspcontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	sdn "github.com/deckhouse/sdn/api/network.deckhouse.io/v1alpha1"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	schemavalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("SDN eligibility", func() {
	It("bounds complete reports for large selections and validates the generated Ready condition schema", func() {
		// The generated CRD is outside this Go module and cannot be embedded.
		data, err := os.ReadFile("../../../../../crds/storage.deckhouse.io_replicatedstoragepools.yaml")
		Expect(err).NotTo(HaveOccurred())
		var crd apiextensionsv1.CustomResourceDefinition
		Expect(yaml.Unmarshal(data, &crd)).To(Succeed())
		conditionSchema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["conditions"].Items.Schema
		var internalSchema apiextensions.JSONSchemaProps
		Expect(apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(conditionSchema, &internalSchema, nil)).To(Succeed())
		validator, _, err := schemavalidation.NewSchemaValidator(&internalSchema)
		Expect(err).NotTo(HaveOccurred())
		legacyDiagnostics := make([]string, 500)
		for i := range legacyDiagnostics {
			legacyDiagnostics[i] = fmt.Sprintf("node-%03d: storage (no usable SystemNetworkIP in NodeNetworkStatus)", i)
		}
		legacyMessage := "No candidate nodes have all required system networks; Excluded nodes without required system networks: " + strings.Join(legacyDiagnostics, "; ")
		Expect(utf8.RuneCountInString(legacyMessage)).To(Equal(34101))
		legacyCondition := map[string]interface{}{
			"type": "Ready", "status": "False", "reason": "NoNodesWithRequiredSystemNetworks",
			"lastTransitionTime": "2026-11-17T00:00:00Z", "message": legacyMessage,
		}
		legacyErrors := schemavalidation.ValidateCustomResource(field.NewPath("status", "conditions").Index(0), legacyCondition, validator)
		Expect(legacyErrors).NotTo(BeEmpty())
		Expect(legacyErrors.ToAggregate().Error()).To(ContainSubstring("32768"))
		for _, longNames := range []bool{false, true} {
			networkName := "storage"
			if longNames {
				networkName = strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
			}
			for _, variant := range []string{"unavailable", "missing", "deleting", "no IP"} {
				for _, partial := range []bool{false, true} {
					pool := &v1alpha1.ReplicatedStoragePool{Spec: v1alpha1.ReplicatedStoragePoolSpec{SystemNetworkNames: []string{networkName}}}
					candidates := make([]v1alpha1.ReplicatedStoragePoolEligibleNode, 501)
					for i := range candidates {
						name := fmt.Sprintf("node-%03d", i)
						if longNames {
							name += "." + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 52)
						}
						candidates[i].NodeName = name
					}
					networks := []sdn.SystemNetwork{{ObjectMeta: metav1.ObjectMeta{Name: networkName}, Spec: sdn.SystemNetworkSpec{IPAM: &sdn.SystemNetworkIPAMSpec{}}}}
					if variant == "missing" {
						networks = nil
					}
					if variant == "deleting" {
						now := metav1.Now()
						networks[0].DeletionTimestamp = &now
					}
					var statuses []sdn.NodeNetworkStatus
					if partial {
						pool.Spec.SystemNetworkNames = []string{networkName}
						// Only the no-IP variant can have a usable candidate.
						if variant != "no IP" {
							continue
						}
						statuses = []sdn.NodeNetworkStatus{{ObjectMeta: metav1.ObjectMeta{Name: candidates[500].NodeName}, Status: sdn.NodeNetworkStatusStatus{Addresses: []sdn.NodeNetworkStatusAddress{{Type: sdn.NodeNetworkStatusAddressTypeSystemNetworkIP, SystemNetworkName: networkName, Address: "10.0.0.1"}}}}}
					}
					eligible, diagnostics := computeTargetNetworkEligibleNodes(pool, candidates, variant != "unavailable", networks, statuses)
					Expect(eligible).To(HaveLen(len(statuses)))
					Expect(diagnostics).To(ContainSubstring(fmt.Sprintf(": %d; examples:", 501-len(statuses))))
					Expect(diagnostics).To(ContainSubstring(candidates[0].NodeName + ":"))
					Expect(diagnostics).To(ContainSubstring(candidates[4].NodeName + ":"))
					Expect(diagnostics).NotTo(ContainSubstring(candidates[5].NodeName + ":"))
					slices.Reverse(candidates)
					_, reversed := computeTargetNetworkEligibleNodes(pool, candidates, variant != "unavailable", networks, statuses)
					Expect(reversed).To(Equal(diagnostics))
					message := computeReadyMessageReport(len(eligible), diagnostics)
					Expect(utf8.ValidString(message)).To(BeTrue())
					Expect(utf8.RuneCountInString(message)).To(BeNumerically("<=", 4096))
					applyPhase(pool, v1alpha1.ReplicatedStoragePoolPhaseReady, message)
					applyReadyCondTrue(pool, v1alpha1.ReplicatedStoragePoolCondReadyReasonReady, message)
					condition := obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType)
					Expect(pool.Status.Message).To(Equal(condition.Message))
					encoded, err := json.Marshal(condition)
					Expect(err).NotTo(HaveOccurred())
					var actual interface{}
					Expect(json.Unmarshal(encoded, &actual)).To(Succeed())
					Expect(schemavalidation.ValidateCustomResource(field.NewPath("status", "conditions").Index(0), actual, validator)).To(BeEmpty())
				}
			}
		}
	})

	It("publishes identical bounded Ready and phase messages for 500 excluded nodes and retains revision semantics", func() {
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(sdn.AddToScheme(scheme)).To(Succeed())
		pool := &v1alpha1.ReplicatedStoragePool{ObjectMeta: metav1.ObjectMeta{Name: "pool"}, Spec: v1alpha1.ReplicatedStoragePoolSpec{SystemNetworkNames: []string{"storage"}}}
		network := &sdn.SystemNetwork{ObjectMeta: metav1.ObjectMeta{Name: "storage"}, Spec: sdn.SystemNetworkSpec{IPAM: &sdn.SystemNetworkIPAMSpec{}}}
		objects := []client.Object{pool, network}
		for i := 0; i < 500; i++ {
			objects = append(objects, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("node-%03d", i)}})
		}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(pool).WithObjects(objects...).Build()
		r := NewReconciler(cl, logr.Discard(), "test", nil)
		r.sdnEnabled = true
		request := reconcile.Request{NamespacedName: client.ObjectKey{Name: pool.Name}}
		_, err := r.Reconcile(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		condition := obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType)
		Expect(condition.Reason).To(Equal("NoNodesWithRequiredSystemNetworks"))
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(pool.Status.Message).To(Equal(condition.Message))
		Expect(utf8.RuneCountInString(condition.Message)).To(BeNumerically("<=", 4096))
		Expect(condition.Message).To(ContainSubstring(": 500; examples:"))
		Expect(pool.Status.EligibleNodes).To(BeEmpty())
		revision := pool.Status.EligibleNodesRevision
		_, err = r.Reconcile(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Status.EligibleNodesRevision).To(Equal(revision))
		Expect(cl.Create(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "usable"}})).To(Succeed())
		Expect(cl.Create(context.Background(), &sdn.NodeNetworkStatus{ObjectMeta: metav1.ObjectMeta{Name: "usable"}, Status: sdn.NodeNetworkStatusStatus{Addresses: []sdn.NodeNetworkStatusAddress{{Type: sdn.NodeNetworkStatusAddressTypeSystemNetworkIP, SystemNetworkName: "storage", Address: "10.0.0.1"}}}})).To(Succeed())
		_, err = r.Reconcile(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		condition = obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType)
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Reason).To(Equal("Ready"))
		Expect(pool.Status.Message).To(Equal(condition.Message))
		Expect(utf8.RuneCountInString(condition.Message)).To(BeNumerically("<=", 4096))
		Expect(condition.Message).To(ContainSubstring(": 500; examples:"))
		Expect(pool.Status.EligibleNodes).To(HaveLen(1))
		Expect(pool.Status.EligibleNodesRevision).To(Equal(revision + 1))
	})

	It("counts Unicode characters and reserves the truncation suffix within the complete report budget", func() {
		diagnostics := "Excluded nodes without required system networks: 500; examples: " + strings.Repeat("界🙂", 4096)
		for _, count := range []int{0, 1} {
			message := computeReadyMessageReport(count, diagnostics)
			Expect(utf8.ValidString(message)).To(BeTrue())
			Expect(utf8.RuneCountInString(message)).To(Equal(4096))
			Expect(message).To(HaveSuffix("... (truncated)"))
			Expect(message).To(ContainSubstring(": 500; examples:"))
		}
	})

	It("adds and removes nodes, reports partial exclusion, and rejects stale addresses after network deletion", func() {
		pool := &v1alpha1.ReplicatedStoragePool{Spec: v1alpha1.ReplicatedStoragePoolSpec{SystemNetworkNames: []string{"storage"}}}
		candidates := []v1alpha1.ReplicatedStoragePoolEligibleNode{{NodeName: "a", NodeReady: true, AgentReady: true, Unschedulable: true}, {NodeName: "b"}}
		networks := []sdn.SystemNetwork{{ObjectMeta: metav1.ObjectMeta{Name: "storage"}, Spec: sdn.SystemNetworkSpec{IPAM: &sdn.SystemNetworkIPAMSpec{}}}}
		nns := []sdn.NodeNetworkStatus{{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Status: sdn.NodeNetworkStatusStatus{Addresses: []sdn.NodeNetworkStatusAddress{{Type: sdn.NodeNetworkStatusAddressTypeSystemNetworkIP, SystemNetworkName: "storage", Address: "10.0.0.1"}}}}}
		eligible, message := computeTargetNetworkEligibleNodes(pool, candidates, true, networks, nns)
		Expect(eligible).To(Equal(candidates[:1]))
		Expect(message).To(ContainSubstring("b: storage"))
		Expect(applyEligibleNodesAndIncrementRevisionIfChanged(pool, eligible)).To(BeTrue())
		Expect(pool.Status.EligibleNodesRevision).To(Equal(int64(1)))
		eligible, message = computeTargetNetworkEligibleNodes(pool, candidates, true, nil, nns)
		Expect(eligible).To(BeEmpty())
		Expect(message).To(ContainSubstring("SystemNetwork missing"))
		Expect(applyEligibleNodesAndIncrementRevisionIfChanged(pool, eligible)).To(BeTrue())
		Expect(pool.Status.EligibleNodes).To(BeEmpty())
		now := metav1.Now()
		networks[0].DeletionTimestamp = &now
		eligible, _ = computeTargetNetworkEligibleNodes(pool, candidates, true, networks, nns)
		Expect(eligible).To(BeEmpty())
		networks[0].DeletionTimestamp = nil
		eligible, _ = computeTargetNetworkEligibleNodes(pool, candidates, true, networks, nns)
		Expect(eligible).To(HaveLen(1))
		nns[0].Status.Addresses[0].Address = "not-an-ip"
		eligible, _ = computeTargetNetworkEligibleNodes(pool, candidates, true, networks, nns)
		Expect(eligible).To(BeEmpty())
	})

	It("does not read SDN while disabled and clears stale nodes with the exact reason", func() {
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		pool := &v1alpha1.ReplicatedStoragePool{ObjectMeta: metav1.ObjectMeta{Name: "pool"}, Spec: v1alpha1.ReplicatedStoragePoolSpec{SystemNetworkNames: []string{"storage"}}, Status: v1alpha1.ReplicatedStoragePoolStatus{EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{{NodeName: "stale"}}}}
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node"}}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(pool).WithObjects(pool, node).Build()
		r := NewReconciler(cl, logr.Discard(), "test", nil)
		_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKey{Name: pool.Name}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Status.EligibleNodes).To(BeEmpty())
		cond := obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType)
		Expect(cond.Reason).To(Equal("NoNodesWithRequiredSystemNetworks"))
		Expect(cond.Message).To(ContainSubstring("SDN unavailable"))
		Expect(cond.Message).NotTo(ContainSubstring("successfully"))
		pool.Spec.SystemNetworkNames = []string{"Internal"}
		Expect(cl.Update(context.Background(), pool)).To(Succeed())
		_, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKey{Name: pool.Name}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Status.EligibleNodes).To(HaveLen(1))
		Expect(obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType).Status).To(Equal(metav1.ConditionTrue))
	})

	It("does not mislabel an empty candidate selection as a network failure", func() {
		pool := &v1alpha1.ReplicatedStoragePool{Spec: v1alpha1.ReplicatedStoragePoolSpec{SystemNetworkNames: []string{"storage"}}}
		_, message := computeTargetNetworkEligibleNodes(pool, nil, false, nil, nil)
		Expect(message).To(BeEmpty())
	})

	It("reconciles address add/remove events and publishes partial diagnostics without a SystemNetwork Ready gate", func() {
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(sdn.AddToScheme(scheme)).To(Succeed())
		pool := &v1alpha1.ReplicatedStoragePool{ObjectMeta: metav1.ObjectMeta{Name: "pool"}, Spec: v1alpha1.ReplicatedStoragePoolSpec{SystemNetworkNames: []string{"storage"}}}
		network := &sdn.SystemNetwork{ObjectMeta: metav1.ObjectMeta{Name: "storage"}, Spec: sdn.SystemNetworkSpec{IPAM: &sdn.SystemNetworkIPAMSpec{}}}
		nns := &sdn.NodeNetworkStatus{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Status: sdn.NodeNetworkStatusStatus{Addresses: []sdn.NodeNetworkStatusAddress{{Type: sdn.NodeNetworkStatusAddressTypeSystemNetworkIP, SystemNetworkName: "storage", Address: "10.0.0.1"}}}}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(pool).WithObjects(pool, network, nns, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "a"}}, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "b"}}).Build()
		r := NewReconciler(cl, logr.Discard(), "test", nil)
		r.sdnEnabled = true
		_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKey{Name: "pool"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Status.EligibleNodes).To(HaveLen(1))
		condition := obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType)
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Message).To(ContainSubstring("b: storage"))
		revision := pool.Status.EligibleNodesRevision
		second := nns.DeepCopy()
		second.Name = "b"
		second.ResourceVersion = ""
		second.Status.Addresses[0].Address = "10.0.0.2"
		Expect(cl.Create(context.Background(), second)).To(Succeed())
		_, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKey{Name: "pool"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Status.EligibleNodes).To(HaveLen(2))
		Expect(pool.Status.EligibleNodesRevision).To(Equal(revision + 1))
		Expect(obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType).Message).NotTo(ContainSubstring("Excluded"))
		Expect(cl.Delete(context.Background(), network)).To(Succeed())
		_, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKey{Name: "pool"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Status.EligibleNodes).To(BeEmpty())
		Expect(pool.Status.EligibleNodesRevision).To(Equal(revision + 2))
		Expect(obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType).Reason).To(Equal("NoNodesWithRequiredSystemNetworks"))
		Expect(obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType).Status).To(Equal(metav1.ConditionFalse))
		network.ResourceVersion = ""
		Expect(cl.Create(context.Background(), network)).To(Succeed())
		_, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKey{Name: "pool"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(pool), pool)).To(Succeed())
		Expect(pool.Status.EligibleNodes).To(HaveLen(2))
		Expect(pool.Status.EligibleNodesRevision).To(Equal(revision + 3))
		Expect(obju.GetStatusCondition(pool, v1alpha1.ReplicatedStoragePoolCondReadyType).Status).To(Equal(metav1.ConditionTrue))
	})

	It("maps address events to SDN pools even with no eligible nodes and scopes network events", func() {
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		pool := &v1alpha1.ReplicatedStoragePool{ObjectMeta: metav1.ObjectMeta{Name: "pool"}, Spec: v1alpha1.ReplicatedStoragePoolSpec{SystemNetworkNames: []string{"storage"}}}
		internal := pool.DeepCopy()
		internal.Name = "internal"
		internal.Spec.SystemNetworkNames = []string{"Internal"}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, internal).Build()
		mapping := mapSDNToRSP(cl)
		Expect(mapping(context.Background(), &sdn.NodeNetworkStatus{})).To(Equal([]reconcile.Request{{NamespacedName: client.ObjectKey{Name: "pool"}}}))
		Expect(mapping(context.Background(), &sdn.SystemNetwork{ObjectMeta: metav1.ObjectMeta{Name: "other"}})).To(BeEmpty())
		Expect(mapping(context.Background(), &sdn.SystemNetwork{ObjectMeta: metav1.ObjectMeta{Name: "storage"}})).To(HaveLen(1))
	})

	It("mechanically detects address, generation, and deletion changes", func() {
		old := &sdn.NodeNetworkStatus{}
		newObj := old.DeepCopy()
		p := nodeNetworkStatusPredicates()[0]
		Expect(p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: newObj})).To(BeFalse())
		newObj.Status.Addresses = []sdn.NodeNetworkStatusAddress{{Address: "10.0.0.1"}}
		Expect(p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: newObj})).To(BeTrue())
		newObj = old.DeepCopy()
		newObj.Generation++
		Expect(p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: newObj})).To(BeTrue())
		network := &sdn.SystemNetwork{}
		updated := network.DeepCopy()
		sp := systemNetworkPredicates()[0]
		Expect(sp.Update(event.UpdateEvent{ObjectOld: network, ObjectNew: updated})).To(BeFalse())
		updated.Generation++
		Expect(sp.Update(event.UpdateEvent{ObjectOld: network, ObjectNew: updated})).To(BeTrue())
		updated = network.DeepCopy()
		now := metav1.Now()
		updated.DeletionTimestamp = &now
		Expect(sp.Update(event.UpdateEvent{ObjectOld: network, ObjectNew: updated})).To(BeTrue())
		Expect(p.Update(event.UpdateEvent{ObjectOld: network, ObjectNew: updated})).To(BeTrue())
	})
})
