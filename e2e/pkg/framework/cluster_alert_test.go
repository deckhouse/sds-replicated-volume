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

package framework

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testLayoutAlert = "D8ReplicatedVolumeLayoutDegraded"

// clusterAlertObject builds one unstructured ClusterAlert the way the
// alerts-receiver writes it.
func clusterAlertObject(objectName, alertName, status string, labels map[string]string) unstructured.Unstructured {
	l := map[string]any{}
	for k, v := range labels {
		l[k] = v
	}
	u := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "deckhouse.io/v1alpha1",
		"kind":       "ClusterAlert",
		"metadata":   map[string]any{"name": objectName},
		"alert": map[string]any{
			"name":          alertName,
			"labels":        l,
			"severityLevel": "6",
		},
		"status": map[string]any{"alertStatus": status},
	}}
	return u
}

// stubLister is the objectLister used by the unit tests: it answers a List with
// a canned set of items or with a canned error.
type stubLister struct {
	items [][]unstructured.Unstructured
	errs  []error
	calls int
}

func (s *stubLister) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	i := s.calls
	s.calls++
	if i >= len(s.items) {
		i = len(s.items) - 1
	}
	if i < len(s.errs) && s.errs[i] != nil {
		return s.errs[i]
	}
	ul, ok := list.(*unstructured.UnstructuredList)
	if !ok {
		return errors.New("stubLister only serves unstructured lists")
	}
	if i >= 0 && i < len(s.items) {
		ul.Items = append([]unstructured.Unstructured(nil), s.items[i]...)
	}
	return nil
}

var _ = Describe("parseClusterAlert", func() {
	It("projects the fields a spec asserts on", func() {
		u := clusterAlertObject("abcdef", testLayoutAlert, ClusterAlertStatusFiring,
			map[string]string{"name": "rv-a", "reason": "TransitionUnsupported"})
		alert, err := parseClusterAlert(&u)
		Expect(err).NotTo(HaveOccurred())
		Expect(alert.ObjectName).To(Equal("abcdef"))
		Expect(alert.Name).To(Equal(testLayoutAlert))
		Expect(alert.Labels).To(Equal(map[string]string{"name": "rv-a", "reason": "TransitionUnsupported"}))
		Expect(alert.SeverityLevel).To(Equal("6"))
		Expect(alert.Status).To(Equal(ClusterAlertStatusFiring))
	})

	It("renders a numeric severityLevel as text", func() {
		u := clusterAlertObject("abcdef", testLayoutAlert, ClusterAlertStatusFiring, nil)
		Expect(unstructured.SetNestedField(u.Object, int64(6), "alert", "severityLevel")).To(Succeed())
		alert, err := parseClusterAlert(&u)
		Expect(err).NotTo(HaveOccurred())
		Expect(alert.SeverityLevel).To(Equal("6"))
	})

	It("tolerates an alert without labels", func() {
		u := clusterAlertObject("abcdef", testLayoutAlert, ClusterAlertStatusFiring, nil)
		unstructured.RemoveNestedField(u.Object, "alert", "labels")
		alert, err := parseClusterAlert(&u)
		Expect(err).NotTo(HaveOccurred())
		Expect(alert.Labels).To(BeEmpty())
	})

	It("fails on an object without alert.name", func() {
		u := clusterAlertObject("abcdef", testLayoutAlert, ClusterAlertStatusFiring, nil)
		unstructured.RemoveNestedField(u.Object, "alert", "name")
		_, err := parseClusterAlert(&u)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("carries no alert.name"))
	})
})

var _ = Describe("selectClusterAlerts", func() {
	alerts := []ClusterAlert{
		{ObjectName: "a", Name: testLayoutAlert, Labels: map[string]string{"name": "rv-a", "reason": "TransitionUnsupported"}},
		{ObjectName: "b", Name: testLayoutAlert, Labels: map[string]string{"name": "rv-b", "reason": "TransitionUnsupported"}},
		{ObjectName: "c", Name: testLayoutAlert, Labels: map[string]string{"name": "rv-a", "reason": "CannotConverge"}},
		{ObjectName: "d", Name: "D8DrbdDeviceHasNoQuorum", Labels: map[string]string{"name": "rv-a"}},
	}

	It("matches the alert with exactly these labels", func() {
		got := selectClusterAlerts(alerts, testLayoutAlert,
			map[string]string{"name": "rv-a", "reason": "TransitionUnsupported"})
		Expect(got).To(HaveLen(1))
		Expect(got[0].ObjectName).To(Equal("a"))
	})

	It("ignores an alert of another name carrying the same labels", func() {
		got := selectClusterAlerts(alerts, "D8DrbdDeviceHasNoQuorum", map[string]string{"name": "rv-a"})
		Expect(got).To(HaveLen(1))
		Expect(got[0].ObjectName).To(Equal("d"))
	})

	It("ignores the same alert carrying different labels", func() {
		got := selectClusterAlerts(alerts, testLayoutAlert,
			map[string]string{"name": "rv-a", "reason": "Converging"})
		Expect(got).To(BeEmpty())
	})

	It("returns every candidate when the selector is partial", func() {
		got := selectClusterAlerts(alerts, testLayoutAlert, map[string]string{"name": "rv-a"})
		Expect(got).To(HaveLen(2))
	})
})

var _ = Describe("clusterAlertsAvailable", func() {
	It("reports true when the list succeeds", func(ctx SpecContext) {
		available, err := clusterAlertsAvailable(ctx, &stubLister{items: [][]unstructured.Unstructured{nil}})
		Expect(err).NotTo(HaveOccurred())
		Expect(available).To(BeTrue())
	})

	It("reports false when the kind is unknown to the cluster", func(ctx SpecContext) {
		noMatch := &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "deckhouse.io", Kind: "ClusterAlert"}}
		s := &stubLister{items: [][]unstructured.Unstructured{nil}, errs: []error{noMatch}}
		available, err := clusterAlertsAvailable(ctx, s)
		Expect(err).NotTo(HaveOccurred())
		Expect(available).To(BeFalse())
	})

	It("reports false when the API server answers NotFound", func(ctx SpecContext) {
		notFound := apierrors.NewNotFound(schema.GroupResource{Group: "deckhouse.io", Resource: "clusteralerts"}, "")
		s := &stubLister{items: [][]unstructured.Unstructured{nil}, errs: []error{notFound}}
		available, err := clusterAlertsAvailable(ctx, s)
		Expect(err).NotTo(HaveOccurred())
		Expect(available).To(BeFalse())
	})

	It("propagates any other error instead of reading it as absence", func(ctx SpecContext) {
		s := &stubLister{items: [][]unstructured.Unstructured{nil}, errs: []error{errors.New("connection refused")}}
		_, err := clusterAlertsAvailable(ctx, s)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connection refused"))
	})
})

var _ = Describe("awaitFiringClusterAlert", func() {
	wanted := map[string]string{"name": "rv-a", "reason": "TransitionUnsupported"}

	It("returns the firing alert with the requested labels", func(ctx SpecContext) {
		s := &stubLister{items: [][]unstructured.Unstructured{{
			clusterAlertObject("other", testLayoutAlert, ClusterAlertStatusFiring,
				map[string]string{"name": "rv-b", "reason": "TransitionUnsupported"}),
			clusterAlertObject("ours", testLayoutAlert, ClusterAlertStatusFiring, wanted),
		}}}
		alert, err := awaitFiringClusterAlert(ctx, s, time.Millisecond, testLayoutAlert, wanted)
		Expect(err).NotTo(HaveOccurred())
		Expect(alert.ObjectName).To(Equal("ours"))
		Expect(alert.SeverityLevel).To(Equal("6"))
	})

	It("keeps polling until the alert shows up", func(ctx SpecContext) {
		s := &stubLister{items: [][]unstructured.Unstructured{
			nil,
			{clusterAlertObject("ours", testLayoutAlert, ClusterAlertStatusFiring, wanted)},
		}}
		_, err := awaitFiringClusterAlert(ctx, s, time.Millisecond, testLayoutAlert, wanted)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.calls).To(Equal(2))
	})

	It("retries a list error instead of failing on it", func(ctx SpecContext) {
		s := &stubLister{
			items: [][]unstructured.Unstructured{
				nil,
				{clusterAlertObject("ours", testLayoutAlert, ClusterAlertStatusFiring, wanted)},
			},
			errs: []error{errors.New("apiserver is busy"), nil},
		}
		_, err := awaitFiringClusterAlert(ctx, s, time.Millisecond, testLayoutAlert, wanted)
		Expect(err).NotTo(HaveOccurred())
	})

	It("does not accept an alert about another volume, nor another alert about this one", func() {
		s := &stubLister{items: [][]unstructured.Unstructured{{
			clusterAlertObject("other-volume", testLayoutAlert, ClusterAlertStatusFiring,
				map[string]string{"name": "rv-b", "reason": "TransitionUnsupported"}),
			clusterAlertObject("other-alert", "D8DrbdDeviceHasNoQuorum", ClusterAlertStatusFiring, wanted),
		}}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := awaitFiringClusterAlert(ctx, s, time.Millisecond, testLayoutAlert, wanted)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no " + testLayoutAlert + " alert carries the labels"))
	})

	It("does not accept a matching alert that is not firing", func() {
		s := &stubLister{items: [][]unstructured.Unstructured{{
			clusterAlertObject("ours", testLayoutAlert, "resolved", wanted),
		}}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := awaitFiringClusterAlert(ctx, s, time.Millisecond, testLayoutAlert, wanted)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("none is firing"))
	})
})
