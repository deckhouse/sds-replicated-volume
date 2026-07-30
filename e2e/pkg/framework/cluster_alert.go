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
	"fmt"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterAlert objects are created by the Deckhouse alerts-receiver from
// Alertmanager webhooks. They are cluster-scoped, have no Go types in this
// module's dependency set, and their name is a hash — so they are read as
// unstructured and found by their alert.name plus alert.labels.
var (
	gvkClusterAlert     = schema.GroupVersionKind{Group: "deckhouse.io", Version: "v1alpha1", Kind: "ClusterAlert"}
	gvkClusterAlertList = schema.GroupVersionKind{Group: "deckhouse.io", Version: "v1alpha1", Kind: "ClusterAlertList"}
)

// ClusterAlertStatusFiring is the only status an alert object is expected to
// carry: a pending alert lives inside Prometheus and is never materialized as an
// object, so an object that exists at all has already passed its `for:` window.
const ClusterAlertStatusFiring = "firing"

// clusterAlertPollInterval is how often AwaitFiringClusterAlert re-lists.
// The wait is dominated by the rule's `for:` window plus a scrape interval, so
// polling faster would only add API traffic.
const clusterAlertPollInterval = 15 * time.Second

// ClusterAlert is the projection of a deckhouse.io ClusterAlert this framework
// reasons about: which alert fired, with which labels, at which severity, and
// whether it is firing right now.
type ClusterAlert struct {
	// ObjectName is metadata.name — a hash, useful only for messages.
	ObjectName string
	// Name is alert.name, i.e. the `alert:` key of the Prometheus rule.
	Name string
	// Labels is alert.labels: the labels the rule expression carries.
	Labels map[string]string
	// SeverityLevel is alert.severityLevel, rendered as text whatever the CRD
	// stores it as.
	SeverityLevel string
	// Status is status.alertStatus (see ClusterAlertStatusFiring).
	Status string
}

// String renders the alert for failure messages.
func (a ClusterAlert) String() string {
	keys := make([]string, 0, len(a.Labels))
	for k := range a.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%q", k, a.Labels[k]))
	}
	return fmt.Sprintf("%s{%v} severity=%s status=%s (object %s)",
		a.Name, pairs, a.SeverityLevel, a.Status, a.ObjectName)
}

// objectLister is the seam the ClusterAlert cores read through. client.Client
// implements it against the cluster; unit tests substitute a stub.
type objectLister interface {
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

// ClusterAlertsAvailable reports whether the cluster serves
// clusteralerts.deckhouse.io at all. A stand without Deckhouse observability
// (no Prometheus, no alerts-receiver) has no such CRD, and a spec whose subject
// is the alerting pipeline has to skip there rather than fail.
func (f *Framework) ClusterAlertsAvailable(ctx context.Context) bool {
	GinkgoHelper()
	available, err := clusterAlertsAvailable(ctx, f.Client)
	if err != nil {
		Fail(err.Error())
	}
	return available
}

// ClusterAlerts returns every ClusterAlert in the cluster, projected.
func (f *Framework) ClusterAlerts(ctx context.Context) []ClusterAlert {
	GinkgoHelper()
	alerts, err := listClusterAlerts(ctx, f.Client)
	if err != nil {
		Fail(err.Error())
	}
	return alerts
}

// AwaitFiringClusterAlert blocks until a firing ClusterAlert named alertName
// carries every label in labels, and returns it. The label match is what makes
// the assertion specific: "some D8ReplicatedVolumeLayoutDegraded is firing"
// would also pass on an alert about a volume the spec never touched.
//
// Alerts materialize only after the rule's `for:` window, so the caller is
// expected to give ctx a budget covering it. Transient List failures are
// retried until the deadline; the last one is reported if the deadline wins.
func (f *Framework) AwaitFiringClusterAlert(
	ctx context.Context,
	alertName string,
	labels map[string]string,
) ClusterAlert {
	GinkgoHelper()
	alert, err := awaitFiringClusterAlert(ctx, f.Client, clusterAlertPollInterval, alertName, labels)
	if err != nil {
		Fail(err.Error())
	}
	return alert
}

// clusterAlertsAvailable is the failing logic of ClusterAlertsAvailable. A
// missing CRD shows up either as a REST-mapping miss (the mapper knows no such
// kind) or as a plain NotFound from the API server; both mean "not installed",
// while anything else is a real error the caller must not read as absence.
func clusterAlertsAvailable(ctx context.Context, lister objectLister) (bool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkClusterAlertList)
	err := lister.List(ctx, list, client.Limit(1))
	switch {
	case err == nil:
		return true, nil
	case meta.IsNoMatchError(err), apierrors.IsNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("probing for %s: %w", gvkClusterAlert.Kind, err)
	}
}

// listClusterAlerts is the failing logic of ClusterAlerts.
func listClusterAlerts(ctx context.Context, lister objectLister) ([]ClusterAlert, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkClusterAlertList)
	if err := lister.List(ctx, list); err != nil {
		return nil, fmt.Errorf("listing %s: %w", gvkClusterAlert.Kind, err)
	}
	out := make([]ClusterAlert, 0, len(list.Items))
	for i := range list.Items {
		alert, err := parseClusterAlert(&list.Items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, alert)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectName < out[j].ObjectName })
	return out, nil
}

// awaitFiringClusterAlert is the failing logic of AwaitFiringClusterAlert. The
// poll interval is a parameter so unit tests drive it without waiting in real
// time.
func awaitFiringClusterAlert(
	ctx context.Context,
	lister objectLister,
	poll time.Duration,
	alertName string,
	labels map[string]string,
) (ClusterAlert, error) {
	for {
		alert, last := findFiringClusterAlert(ctx, lister, alertName, labels)
		if last == nil {
			return alert, nil
		}
		select {
		case <-ctx.Done():
			return ClusterAlert{}, fmt.Errorf("timed out waiting for a firing %s alert with labels %v: %w",
				alertName, labels, last)
		case <-time.After(poll):
		}
	}
}

// findFiringClusterAlert returns the first firing alert matching alertName and
// labels, or the reason there is none. Several firing copies of the same alert
// are not an error: the spec's claim is "this volume raised this alert", and the
// object count is Alertmanager's business.
func findFiringClusterAlert(
	ctx context.Context,
	lister objectLister,
	alertName string,
	labels map[string]string,
) (ClusterAlert, error) {
	alerts, err := listClusterAlerts(ctx, lister)
	if err != nil {
		return ClusterAlert{}, err
	}
	matching := selectClusterAlerts(alerts, alertName, labels)
	for _, alert := range matching {
		if alert.Status == ClusterAlertStatusFiring {
			return alert, nil
		}
	}
	if len(matching) > 0 {
		return ClusterAlert{}, fmt.Errorf("%d %s alert(s) carry the labels %v but none is %s: %v",
			len(matching), alertName, labels, ClusterAlertStatusFiring, matching)
	}
	return ClusterAlert{}, fmt.Errorf("no %s alert carries the labels %v (%d ClusterAlerts in the cluster)",
		alertName, labels, len(alerts))
}

// selectClusterAlerts returns the alerts whose alert.name equals name and whose
// alert.labels contain every pair in labels. The label match is a subset match:
// the rule's labels reach the object alongside the ones Alertmanager adds
// (severity_level, tier, ...), and a spec only names the ones it reasons about.
func selectClusterAlerts(alerts []ClusterAlert, name string, labels map[string]string) []ClusterAlert {
	var out []ClusterAlert
	for _, alert := range alerts {
		if alert.Name != name {
			continue
		}
		matched := true
		for k, v := range labels {
			if alert.Labels[k] != v {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, alert)
		}
	}
	return out
}

// parseClusterAlert projects one unstructured ClusterAlert. A missing
// alert.name is an error: an object of this kind without it cannot be matched
// against anything, and silently treating it as "some other alert" would let a
// malformed object hide the one the spec is waiting for.
func parseClusterAlert(u *unstructured.Unstructured) (ClusterAlert, error) {
	out := ClusterAlert{ObjectName: u.GetName(), Labels: map[string]string{}}

	name, found, err := unstructured.NestedString(u.Object, "alert", "name")
	if err != nil {
		return ClusterAlert{}, fmt.Errorf("reading alert.name of ClusterAlert %s: %w", u.GetName(), err)
	}
	if !found {
		return ClusterAlert{}, fmt.Errorf("ClusterAlert %s carries no alert.name", u.GetName())
	}
	out.Name = name

	labels, _, err := unstructured.NestedStringMap(u.Object, "alert", "labels")
	if err != nil {
		return ClusterAlert{}, fmt.Errorf("reading alert.labels of ClusterAlert %s: %w", u.GetName(), err)
	}
	if labels != nil {
		out.Labels = labels
	}

	out.SeverityLevel = nestedScalarString(u.Object, "alert", "severityLevel")
	out.Status = nestedScalarString(u.Object, "status", "alertStatus")
	return out, nil
}

// nestedScalarString reads a scalar field as text, whatever the CRD stores it
// as. severityLevel in particular is a string in the shipped CRD but reads as a
// number in hand-written manifests, and the spec compares it to "6" either way.
// An absent field is an empty string.
func nestedScalarString(obj map[string]any, fields ...string) string {
	value, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if err != nil || !found || value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", value)
}
