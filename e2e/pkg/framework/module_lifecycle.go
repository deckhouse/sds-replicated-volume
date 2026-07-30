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
	"fmt"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The Deckhouse objects this helper writes to pin a module to a dev build
// (ModuleConfig, ModulePullOverride) and the one it reads for Deckhouse's own
// view of that module (Module). None of these kinds has Go types in this
// module's dependency set — and adding them would drag the whole Deckhouse API
// in — so all three are built and read as unstructured.
var (
	gvkModuleConfig       = schema.GroupVersionKind{Group: "deckhouse.io", Version: "v1alpha1", Kind: "ModuleConfig"}
	gvkModulePullOverride = schema.GroupVersionKind{Group: "deckhouse.io", Version: "v1alpha2", Kind: "ModulePullOverride"}
	gvkModule             = schema.GroupVersionKind{Group: "deckhouse.io", Version: "v1alpha1", Kind: "Module"}
)

const (
	// ModuleName is the Deckhouse module this framework drives. It is also the
	// name of its ModuleConfig and of its ModulePullOverride: Deckhouse keys both
	// by module name.
	ModuleName = "sds-replicated-volume"

	// moduleNamespace is where the module's workloads live. Same namespace as
	// controllerMetricsNamespace (controller_metrics.go) — spelled out separately
	// because that one is part of the metrics endpoint's address, while this one
	// is the namespace whose workloads prove the module is running.
	moduleNamespace = "d8-sds-replicated-volume"

	// modulePullOverrideScanInterval is how often Deckhouse re-resolves the tag
	// to a digest. 15s matches the documented dev-install recipe: the retag of a
	// live MPO must be noticed in seconds, not on a multi-minute default.
	modulePullOverrideScanInterval = "15s"

	// DefaultModuleReadyTimeout is the readiness budget EnsureModuleVersion uses
	// when the caller passes none. A retag pulls a fresh bundle and restarts every
	// workload of the module, so the wait is dominated by image pulls.
	DefaultModuleReadyTimeout = 15 * time.Minute

	// moduleReadyPollInterval is how often the readiness criterion is re-read.
	// The wait is dominated by pulls and pod restarts, so polling faster would
	// only add API traffic.
	moduleReadyPollInterval = 10 * time.Second

	// moduleObservationWindow is how long the accepted state has to stay
	// UNCHANGED before the module counts as rolled out.
	//
	// The Module fields the criterion gates on are published by Deckhouse, which
	// means they can be a moment ahead of what the workloads show: the phase is
	// written when the module's run finished, and the generations that run bumped
	// are read through a different API path. The window is the slack for that
	// skew, and it is short on purpose — the ORDERING against the re-apply is
	// proven by properties.version and the IsReady transition (see
	// EnsureModuleVersion), not by waiting long enough.
	moduleObservationWindow = 10 * time.Second

	// moduleReadyPhase is the Module status.phase Deckhouse publishes once a
	// module's run — the run that applies its Helm manifests — has finished.
	moduleReadyPhase = "Ready"

	// moduleReadyConditionType is the Module condition whose lastTransitionTime
	// the criterion reads. Deckhouse takes it out of True before a module's run
	// and puts it back afterwards, so its transition is what tells "the module
	// ran again" apart from "the module was Ready before all this started".
	moduleReadyConditionType = "IsReady"
)

// moduleWaitPolicy budgets the readiness wait: the whole wait, one poll, and how
// long an accepted state must hold. They travel together and named because three
// positional time.Duration arguments are three chances to swap two of them.
type moduleWaitPolicy struct {
	// Timeout budgets the whole wait; see DefaultModuleReadyTimeout.
	Timeout time.Duration
	// Poll is how often the criterion is re-read; see moduleReadyPollInterval.
	Poll time.Duration
	// ObservationWindow is how long the accepted state must stay unchanged; see
	// moduleObservationWindow.
	ObservationWindow time.Duration
}

// moduleWorkloadKind names the controller that owns a workload, because the
// rollout counters live under different names for each.
type moduleWorkloadKind string

const (
	workloadDeployment moduleWorkloadKind = "Deployment"
	workloadDaemonSet  moduleWorkloadKind = "DaemonSet"
)

// moduleWorkload identifies one workload of the module.
type moduleWorkload struct {
	Kind moduleWorkloadKind
	Name string
}

// String renders the workload the way kubectl addresses it (deployment/agent).
func (w moduleWorkload) String() string {
	return strings.ToLower(string(w.Kind)) + "/" + w.Name
}

// moduleWorkloads lists every workload the module ships with the new control
// plane, in the order the dev-install runbook checks them. The list is the
// readiness criterion's subject: waiting for "the workloads in the namespace" to
// be rollout-complete is vacuously true on a stand where the module is not
// installed at all, so the expected set has to be named.
func moduleWorkloads() []moduleWorkload {
	return []moduleWorkload{
		{Kind: workloadDeployment, Name: "controller"},
		{Kind: workloadDeployment, Name: "csi-controller"},
		{Kind: workloadDeployment, Name: "spaas"},
		{Kind: workloadDeployment, Name: "webhooks"},
		{Kind: workloadDaemonSet, Name: "agent"},
		{Kind: workloadDaemonSet, Name: "csi-node"},
	}
}

// ---------------------------------------------------------------------------
// Exported helpers
// ---------------------------------------------------------------------------

// EnsureModuleVersion installs the Deckhouse module moduleName at imageTag and
// blocks until the module actually runs that build.
//
// It writes exactly two objects, create-or-update:
//
//   - ModuleConfig (deckhouse.io/v1alpha1) with spec.enabled=true. ONLY
//     spec.enabled is touched, so a stand's own settings and version survive,
//     and an already-enabled ModuleConfig is not written at all. Enabling is not
//     optional: Deckhouse ignores the ModulePullOverride of a disabled module,
//     which is why both objects are submitted before anything is awaited.
//   - ModulePullOverride (deckhouse.io/v1alpha2) with spec.imageTag=imageTag,
//     rollback=false and scanInterval=15s. Calling the helper again with a
//     DIFFERENT tag retags the live object — that retag IS the module upgrade.
//
// Readiness is judged by Deckhouse's own view of the module and by the
// workloads, never by the tag inside a pod's image: werf renders images
// content-addressed (<registry>/<module>@sha256:<digest>), so no pod ever
// mentions the tag. All of these must hold at once:
//
//   - status.imageDigest of the MPO shows the transition the state before the
//     write implies (moduleDigestRequirement): a digest DIFFERENT from the one
//     observed before, when a live override was retagged, and merely a PUBLISHED
//     one when there was no override at all. That bundle digest is the FIRST
//     signal in the cluster that Deckhouse has looked at the override; an absent
//     or empty digest means "no signal yet" and the wait continues. Requiring it
//     on the create path is what keeps a first installation over an ALREADY
//     RUNNING module honest — a module installed from a release channel keeps its
//     healthy workloads while the new override is picked up, and accepting them
//     would report a version the module is not running.
//   - the Module resource reports properties.version equal to imageTag. Writing
//     the digest is not the end of the retag but the middle of it: the override
//     controller restarts Deckhouse itself right afterwards, and the restarted
//     process writes properties.version from the override's tag while it syncs the
//     filesystem with the cluster — strictly BEFORE it runs a single module. The
//     tag in that field is therefore the proof that the Deckhouse which will
//     re-apply the manifests is already the one that read our override.
//   - the module's readiness condition (moduleReadyConditionType) carries a
//     lastTransitionTime OTHER than the one observed before the write, whenever a
//     digest transition is required. The converge after the restart takes the
//     module out of ready before its run and puts it back afterwards, so a
//     changed transition is the proof that the module ran AGAIN. The phase alone
//     cannot give that: a phase written BEFORE the restart stays readable
//     throughout it.
//   - status.phase is Ready — the run that re-applied the manifests finished.
//   - every workload of moduleWorkloads() exists in d8-sds-replicated-volume —
//     "the rollout is complete" over an empty namespace is vacuously true and is
//     therefore rejected, which is what makes the criterion usable for a first
//     installation;
//   - each workload is rollout-complete (its controller observed the current
//     generation, every pod runs the current template, no pod of an older
//     template is left) and reports its pods available and ready.
//
// And the whole of it has to hold UNCHANGED over an observation window
// (moduleObservationWindow, moduleStableSamples): the Module fields are published
// by Deckhouse and can be a moment ahead of what the workloads show, so a state
// is believed only if nothing moved while the window ran out — and anything that
// moves starts it over.
//
// Pod-template digests are reported as progress but deliberately NOT required to
// change: werf builds are content-addressed, so a component untouched between
// the two tags keeps its digest, and demanding a change would hang forever on two
// close dev builds — the very reason the ordering is proven out of the Module's
// fields instead.
//
// Those fields are also the criterion's only dependency on Deckhouse internals,
// and the trade is deliberate: a module Deckhouse would never pin to a tag (an
// embedded one, whose override it ignores) or never publish as Ready (one that
// leaves its readiness to a hook) times the wait out with the unmet gate spelled
// out in the message. A timeout says where to look; a false "ready" would
// silently invalidate everything the caller asserts afterwards.
//
// Idempotent, and safe to run concurrently with itself on the same tag — which
// is what makes it usable as a pre-discovery hook Ginkgo runs once per worker
// (see WithPreDiscovery). A repeat call with the tag already in the live MPO
// writes nothing and only re-checks the rollout. A concurrent call that wins a
// write leaves this one with AlreadyExists (both created) or Conflict (both
// updated); both mean "somebody wrote what I was about to write", so the object
// is re-read and the decision retaken instead of failing the suite (see
// retryOnModuleWriteRace). What is AWAITED is derived from the state observed
// before the write, so the caller whose write was won still waits for the digest
// transition rather than accepting the build the stand ran before.
//
// timeout budgets the whole readiness wait; 0 means DefaultModuleReadyTimeout.
// NOTHING is cleaned up and no DeferCleanup is registered: the module is left
// installed at imageTag on purpose — the state of the stand after a run is the
// diagnosis material, and retagging it back would destroy it.
//
// It is the one helper that damages state shared with the whole cluster and does
// NOT call RequireDisruptiveSpec, because its designed call site is a
// pre-discovery hook — a suite-level node, where that guard refuses by
// construction: such a node takes no decorators, so the label it would demand can
// never be written on it. The requirement is met one level up instead. A suite
// that retags a shared module carries LabelDisruptive on its container and
// refuses to start at all when the class is off (DisruptiveEnabled), which is
// strictly earlier than a call-site check would fire.
func (f *Framework) EnsureModuleVersion(ctx context.Context, moduleName, imageTag string, timeout time.Duration) {
	GinkgoHelper()
	if timeout <= 0 {
		timeout = DefaultModuleReadyTimeout
	}
	err := ensureModuleVersion(ctx, f.Client, f, moduleName, imageTag, moduleWaitPolicy{
		Timeout:           timeout,
		Poll:              moduleReadyPollInterval,
		ObservationWindow: moduleObservationWindow,
	})
	if err != nil {
		Fail(err.Error())
	}
}

// ValidateModuleImageTag reports whether imageTag can be used as spec.imageTag of
// a ModulePullOverride: a non-empty string of printable ASCII characters, with no
// spaces and no control characters.
//
// The rule is deliberately loose about SHAPE. The dev tags of this project are
// arbitrary — pr758 built by CI from a pull request, main, a branch name — so a
// pattern like `pr<N>|main` would refuse tags that work. What it does catch is
// the class of mistakes made while typing a variable on a command line: an empty
// value, a quoted value that kept its spaces, a trailing newline, a character no
// registry would accept in a reference.
//
// It returns an error instead of failing the spec, for the same reason
// ParseVolumesOverride does: its caller is a suite's entry point (func TestX,
// before RunSpecs), where Fail and Skip panic because no node is running. The
// text is a sentence fragment ("must not be empty"), so both that caller and
// ensureModuleVersion can prefix it with the subject they are talking about.
//
// ensureModuleVersion validates through this very function, so a tag a gate
// accepted is a tag the helper accepts too.
func ValidateModuleImageTag(imageTag string) error {
	if imageTag == "" {
		return errors.New("must not be empty")
	}
	for _, r := range imageTag {
		// Printable ASCII without the space: '!' (0x21) through '~' (0x7e).
		if r < '!' || r > '~' {
			return fmt.Errorf(
				"must consist of printable ASCII characters without spaces, got %q", imageTag)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Seams
// ---------------------------------------------------------------------------

// moduleObjectClient is the seam the write path goes through. client.Client
// implements it against the cluster; helper unit tests substitute the
// controller-runtime fake client, so create-or-update is exercised — with real
// NotFound and resourceVersion semantics — without a cluster.
type moduleObjectClient interface {
	Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error
	Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
	Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error
}

// moduleObserver is the seam the readiness poll reads through. *Framework reads
// one snapshot per poll with the framework client; unit tests script the
// snapshots, so every branch of the criterion is walked without a cluster.
type moduleObserver interface {
	observeModule(ctx context.Context, moduleName string) (moduleObservation, error)
}

// ---------------------------------------------------------------------------
// Observation
// ---------------------------------------------------------------------------

// moduleObservation is one snapshot of everything the readiness criterion reads:
// the pull override's tag and published bundle digest, Deckhouse's own view of
// the module, plus the state of every expected workload.
type moduleObservation struct {
	// MPOFound reports whether a ModulePullOverride exists at all.
	MPOFound bool
	// MPOTag is spec.imageTag of that object ("" when there is none).
	MPOTag string
	// MPODigest is status.imageDigest — the digest of the bundle Deckhouse
	// resolved the tag to. "" means Deckhouse has not published one yet.
	MPODigest string

	// ModuleFound reports whether the Module resource exists at all.
	ModuleFound bool
	// ModulePhase is status.phase of the Module ("" when there is none).
	ModulePhase string
	// ModuleVersion is properties.version — for an overridden module, the tag
	// the running Deckhouse restored the module from.
	ModuleVersion string
	// ModuleReadyTransition is lastTransitionTime of the module's
	// moduleReadyConditionType condition, verbatim ("" when the condition is not
	// there). Compared, never parsed: only whether it CHANGED matters, and the
	// value is written by the cluster's clock, not ours.
	ModuleReadyTransition string

	// Workloads holds one entry per moduleWorkloads() entry, in that order,
	// present or not.
	Workloads []moduleWorkloadState
}

// moduleWorkloadState is one workload's rollout state, with the Deployment and
// DaemonSet counters projected onto one set of names.
type moduleWorkloadState struct {
	Workload moduleWorkload
	// Found reports whether the object exists.
	Found bool

	Generation         int64
	ObservedGeneration int64

	// Desired is how many pods the workload wants (spec.replicas /
	// status.desiredNumberScheduled).
	Desired int32
	// Updated is how many of them run the current pod template.
	Updated int32
	// Current is how many pods exist for the workload, on any template.
	Current int32
	// Ready and Available are the readiness the workload's controller publishes
	// for its pods.
	Ready     int32
	Available int32

	// Images is the sorted pod-template image set (init containers included).
	// werf renders every image as <registry>/<module>@sha256:<digest>, so this
	// slice IS the pod-template digest set a retag may change.
	Images []string
}

// rolloutError reports nil when the workload finished rolling out and all of its
// pods are up, and otherwise the reason it did not — phrased for a timeout
// message.
func (s moduleWorkloadState) rolloutError() error {
	switch {
	case !s.Found:
		return fmt.Errorf("%s does not exist yet", s.Workload)
	case s.ObservedGeneration < s.Generation:
		return fmt.Errorf("%s: its controller is still at observedGeneration %d, the object is at generation %d",
			s.Workload, s.ObservedGeneration, s.Generation)
	case s.Desired == 0:
		return fmt.Errorf("%s wants 0 pods, so its readiness would prove nothing", s.Workload)
	case s.Updated != s.Desired:
		return fmt.Errorf("%s: %d of %d pods run the current template", s.Workload, s.Updated, s.Desired)
	case s.Current != s.Updated:
		return fmt.Errorf("%s: %d pods exist against %d on the current template, an older template is still around",
			s.Workload, s.Current, s.Updated)
	case s.Available < s.Desired:
		return fmt.Errorf("%s: %d of %d pods available", s.Workload, s.Available, s.Desired)
	case s.Ready < s.Desired:
		return fmt.Errorf("%s: %d of %d pods ready", s.Workload, s.Ready, s.Desired)
	}
	return nil
}

// observeModule reads one moduleObservation through the framework client.
//
// Both Deckhouse objects are read as unstructured, which the framework client
// serves straight from the API server (its cache covers typed objects only) — so
// the poll sees status.imageDigest, and the Module's phase, as soon as Deckhouse
// writes them.
func (f *Framework) observeModule(ctx context.Context, moduleName string) (moduleObservation, error) {
	var out moduleObservation

	mpo := &unstructured.Unstructured{}
	mpo.SetGroupVersionKind(gvkModulePullOverride)
	err := f.Client.Get(ctx, client.ObjectKey{Name: moduleName}, mpo)
	switch {
	case err == nil:
		out.MPOFound = true
		out.MPOTag = nestedScalarString(mpo.Object, "spec", "imageTag")
		out.MPODigest = nestedScalarString(mpo.Object, "status", "imageDigest")
	case apierrors.IsNotFound(err):
		// No override yet — the normal state of a first installation.
	default:
		return moduleObservation{}, fmt.Errorf("reading %s %q: %w", gvkModulePullOverride.Kind, moduleName, err)
	}

	module := &unstructured.Unstructured{}
	module.SetGroupVersionKind(gvkModule)
	err = f.Client.Get(ctx, client.ObjectKey{Name: moduleName}, module)
	switch {
	case err == nil:
		out.ModuleFound = true
		out.ModuleVersion = nestedScalarString(module.Object, "properties", "version")
		out.ModulePhase = nestedScalarString(module.Object, "status", "phase")
		out.ModuleReadyTransition = moduleConditionTransition(module.Object, moduleReadyConditionType)
	case apierrors.IsNotFound(err):
		// Deckhouse creates the Module when the source publishing it is scanned,
		// so a stand that never saw the module has none.
	default:
		return moduleObservation{}, fmt.Errorf("reading %s %q: %w", gvkModule.Kind, moduleName, err)
	}

	var deployments appsv1.DeploymentList
	if err := f.Client.List(ctx, &deployments, client.InNamespace(moduleNamespace)); err != nil {
		return moduleObservation{}, fmt.Errorf("listing Deployments in %s: %w", moduleNamespace, err)
	}
	var daemonSets appsv1.DaemonSetList
	if err := f.Client.List(ctx, &daemonSets, client.InNamespace(moduleNamespace)); err != nil {
		return moduleObservation{}, fmt.Errorf("listing DaemonSets in %s: %w", moduleNamespace, err)
	}
	out.Workloads = projectModuleWorkloads(moduleWorkloads(), &deployments, &daemonSets)

	return out, nil
}

// moduleConditionTransition reads lastTransitionTime of one Module condition,
// verbatim, and answers "" for a condition (or a status) that is not there. An
// absent value is a legitimate observation, not an error: the criterion only ever
// compares two of these, and "it was not there before either" is exactly what an
// empty pair says.
func moduleConditionTransition(obj map[string]any, conditionType string) string {
	conditions, found, err := unstructured.NestedSlice(obj, "status", "conditions")
	if err != nil || !found {
		return ""
	}
	for _, entry := range conditions {
		condition, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if nestedScalarString(condition, "type") == conditionType {
			return nestedScalarString(condition, "lastTransitionTime")
		}
	}
	return ""
}

// projectModuleWorkloads projects the expected workloads onto what the cluster
// currently holds. A workload nobody found stays in the result as Found=false:
// the criterion has to be able to say WHICH workload is missing.
func projectModuleWorkloads(
	want []moduleWorkload,
	deployments *appsv1.DeploymentList,
	daemonSets *appsv1.DaemonSetList,
) []moduleWorkloadState {
	out := make([]moduleWorkloadState, 0, len(want))
	for _, w := range want {
		state := moduleWorkloadState{Workload: w}
		switch w.Kind {
		case workloadDeployment:
			for i := range deployments.Items {
				if deployments.Items[i].Name == w.Name {
					state = deploymentState(w, &deployments.Items[i])
					break
				}
			}
		case workloadDaemonSet:
			for i := range daemonSets.Items {
				if daemonSets.Items[i].Name == w.Name {
					state = daemonSetState(w, &daemonSets.Items[i])
					break
				}
			}
		}
		out = append(out, state)
	}
	return out
}

// deploymentState projects a Deployment's rollout counters. An unset
// spec.replicas defaults to 1, exactly as the API server does.
func deploymentState(w moduleWorkload, d *appsv1.Deployment) moduleWorkloadState {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	return moduleWorkloadState{
		Workload:           w,
		Found:              true,
		Generation:         d.Generation,
		ObservedGeneration: d.Status.ObservedGeneration,
		Desired:            desired,
		Updated:            d.Status.UpdatedReplicas,
		Current:            d.Status.Replicas,
		Ready:              d.Status.ReadyReplicas,
		Available:          d.Status.AvailableReplicas,
		Images:             podTemplateImages(&d.Spec.Template),
	}
}

// daemonSetState projects a DaemonSet's rollout counters. Desired comes from the
// status, because the DaemonSet controller — not the author — decides how many
// nodes the set covers.
func daemonSetState(w moduleWorkload, ds *appsv1.DaemonSet) moduleWorkloadState {
	return moduleWorkloadState{
		Workload:           w,
		Found:              true,
		Generation:         ds.Generation,
		ObservedGeneration: ds.Status.ObservedGeneration,
		Desired:            ds.Status.DesiredNumberScheduled,
		Updated:            ds.Status.UpdatedNumberScheduled,
		Current:            ds.Status.CurrentNumberScheduled,
		Ready:              ds.Status.NumberReady,
		Available:          ds.Status.NumberAvailable,
		Images:             podTemplateImages(&ds.Spec.Template),
	}
}

// podTemplateImages returns the sorted image set of a pod template, init
// containers included.
func podTemplateImages(template *corev1.PodTemplateSpec) []string {
	out := make([]string, 0, len(template.Spec.InitContainers)+len(template.Spec.Containers))
	for i := range template.Spec.InitContainers {
		out = append(out, template.Spec.InitContainers[i].Image)
	}
	for i := range template.Spec.Containers {
		out = append(out, template.Spec.Containers[i].Image)
	}
	slices.Sort(out)
	return out
}

// ---------------------------------------------------------------------------
// Readiness criterion
// ---------------------------------------------------------------------------

// moduleDigestRequirement is what the readiness criterion demands of
// ModulePullOverride status.imageDigest — the digest of the module bundle
// Deckhouse resolved the tag to, and the only signal in the cluster that
// Deckhouse has looked at the override at all.
type moduleDigestRequirement int

const (
	// digestIgnored demands nothing: the override already pinned the tag before
	// the call, so no digest transition is coming and the rollout of the
	// workloads is the whole criterion.
	digestIgnored moduleDigestRequirement = iota

	// digestPublished demands a non-empty digest. There was no override before
	// the call, so ANY digest is one Deckhouse published for ours — and until it
	// appears, the workloads a poll sees are the ones of whatever ran BEFORE the
	// override. On a stand that already runs the module from a release channel
	// those workloads are perfectly healthy, so a criterion that ignores the
	// digest here would report "ready" on the OLD build the instant the override
	// was created, before Deckhouse noticed it. On a genuinely first installation
	// the demand costs nothing: Deckhouse resolves the tag and publishes the
	// digest before a single workload of the module exists. A Deckhouse that never
	// publishes the field at all would time the wait out with that reason spelled
	// in the message — the same dependency the retag path already lives with, and
	// status.imageDigest is present on the v1alpha2 override this suite targets.
	digestPublished

	// digestChanged demands a non-empty digest OTHER than the one observed before
	// the write: a live override was retagged, so the digest it carried belongs to
	// the previous bundle and only a different one proves the new one was pulled.
	digestChanged
)

// String renders the requirement for the progress line.
func (r moduleDigestRequirement) String() string {
	switch r {
	case digestPublished:
		return "published"
	case digestChanged:
		return "changed"
	default:
		return "not required"
	}
}

// moduleReadiness is what one readiness poll is waiting for: the rollout of
// every expected workload, the state Deckhouse has to publish for the module, and
// the transitions the state before the write implies.
type moduleReadiness struct {
	// DigestBefore is MPO status.imageDigest as observed BEFORE the write ("" if
	// there was no override or no digest yet).
	DigestBefore string
	// Digest is what status.imageDigest has to show — see
	// moduleDigestRequirement and moduleDigestRequirementFor.
	Digest moduleDigestRequirement
	// ImageTag is the tag the override pins, and therefore the version the
	// restarted Deckhouse has to report for the module. An empty value means the
	// gate is not applied, which is a convenience for tests about the other
	// gates; ensureModuleVersion always fills it in.
	ImageTag string
	// ReadyTransitionBefore is moduleObservation.ModuleReadyTransition as
	// observed BEFORE the write. Required to have changed exactly when a digest
	// transition is required — the same reasoning as DigestBefore: nothing is
	// re-applied on the path where the tag was already pinned, so demanding a
	// transition there would wait forever.
	ReadyTransitionBefore string
	// ImagesBefore is the pod-template image set per workload before the write,
	// keyed by workload. Progress reporting only — see moduleImagesChanged.
	ImagesBefore map[string][]string
}

// moduleDigestRequirementFor decides what the criterion demands of the bundle
// digest, from the state observed BEFORE the write rather than from what the
// write turned out to do.
//
// The two differ only when a concurrent caller wrote the same tag first (see
// retryOnModuleWriteRace): that write path then reports "unchanged", while the
// cluster still owes THIS caller the transition its own snapshot did not have.
// Deriving the demand from the snapshot keeps every racing caller waiting for the
// same thing instead of letting the one that lost the write accept the build the
// stand ran before.
func moduleDigestRequirementFor(before moduleObservation, imageTag string) moduleDigestRequirement {
	switch {
	case !before.MPOFound:
		return digestPublished
	case before.MPOTag != imageTag:
		return digestChanged
	default:
		return digestIgnored
	}
}

// checkModuleReady reports nil when the observation satisfies the criterion, and
// otherwise the reason it does not — the message a timeout would carry.
//
// The gates are ordered along the chain a retag actually travels: Deckhouse
// resolves the tag to a bundle, restarts and restores the module from that
// bundle, runs the module, and the run's manifests roll the workloads. So the
// message names the EARLIEST link that has not happened yet, which is the one
// worth looking at.
func checkModuleReady(want moduleReadiness, obs moduleObservation) error {
	if want.Digest != digestIgnored && obs.MPODigest == "" {
		return fmt.Errorf("%s carries no status.imageDigest yet", gvkModulePullOverride.Kind)
	}
	if want.Digest == digestChanged && obs.MPODigest == want.DigestBefore {
		return fmt.Errorf("%s still reports the bundle digest it had before the retag (%s)",
			gvkModulePullOverride.Kind, want.DigestBefore)
	}
	if !obs.ModuleFound {
		return fmt.Errorf("the %s resource does not exist yet", gvkModule.Kind)
	}
	if want.ImageTag != "" && obs.ModuleVersion != want.ImageTag {
		return fmt.Errorf("%s reports properties.version %q, not the pinned tag %q,"+
			" so the Deckhouse that will re-apply the manifests has not restored the override yet",
			gvkModule.Kind, obs.ModuleVersion, want.ImageTag)
	}
	if want.Digest != digestIgnored && obs.ModuleReadyTransition == want.ReadyTransitionBefore {
		return fmt.Errorf("the %s condition of %s still carries the transition it had before the write (%q),"+
			" so the module has not run again yet",
			moduleReadyConditionType, gvkModule.Kind, want.ReadyTransitionBefore)
	}
	if obs.ModulePhase != moduleReadyPhase {
		return fmt.Errorf("%s is in phase %q, not %q", gvkModule.Kind, obs.ModulePhase, moduleReadyPhase)
	}
	// An observation with no workloads at all would make every rollout claim
	// below vacuously true.
	if len(obs.Workloads) == 0 {
		return errors.New("no workload of the module was observed at all")
	}
	for _, state := range obs.Workloads {
		if err := state.rolloutError(); err != nil {
			return err
		}
	}
	return nil
}

// moduleStateFingerprint renders everything the observation window watches for
// movement: Deckhouse's view of the module and the rollout state of every
// workload. Two samples with the same fingerprint mean nothing moved between
// them; any difference means a re-apply started or finished after the state was
// accepted, and the window starts over.
//
// It is compared, never read, so the format only has to be stable and complete —
// the workload order is moduleWorkloads()' own, which projectModuleWorkloads
// preserves.
func moduleStateFingerprint(obs moduleObservation) string {
	var out strings.Builder
	fmt.Fprintf(&out, "mpo=%t/%s/%s module=%t/%s/%s/%s",
		obs.MPOFound, obs.MPOTag, obs.MPODigest,
		obs.ModuleFound, obs.ModuleVersion, obs.ModulePhase, obs.ModuleReadyTransition)
	for _, state := range obs.Workloads {
		fmt.Fprintf(&out, " %s=%t/%d/%d/%d/%d/%d/%d/%d/%s",
			state.Workload, state.Found,
			state.Generation, state.ObservedGeneration,
			state.Desired, state.Updated, state.Current, state.Ready, state.Available,
			strings.Join(state.Images, ","))
	}
	return out.String()
}

// moduleImagesChanged returns the workloads whose pod-template image set differs
// from the pre-write snapshot.
//
// Reported, never required: werf builds are content-addressed, so a component
// that did not change between the two tags keeps its digest and its pod template
// — requiring a change for every workload (or even for one) would hang on close
// dev builds. It is printed so that a run against two tags that DO differ shows
// which components were actually replaced.
func moduleImagesChanged(before map[string][]string, obs moduleObservation) []string {
	var out []string
	for _, state := range obs.Workloads {
		if !state.Found {
			continue
		}
		if !slices.Equal(before[state.Workload.String()], state.Images) {
			out = append(out, state.Workload.String())
		}
	}
	return out
}

// observationImages projects an observation into the per-workload image sets
// moduleReadiness compares against.
func observationImages(obs moduleObservation) map[string][]string {
	out := make(map[string][]string, len(obs.Workloads))
	for _, state := range obs.Workloads {
		if state.Found {
			out[state.Workload.String()] = state.Images
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Core
// ---------------------------------------------------------------------------

// moduleObjectAction is what the write path did to one object.
type moduleObjectAction string

const (
	moduleObjectCreated   moduleObjectAction = "created"
	moduleObjectUpdated   moduleObjectAction = "updated"
	moduleObjectUnchanged moduleObjectAction = "unchanged"
)

// ensureModuleVersion is the failing logic of EnsureModuleVersion: snapshot,
// write both objects, then wait for the rollout the write implies.
//
// The snapshot is taken BEFORE the write on purpose — it is the only moment at
// which the digest of the OLD bundle can still be read, and that value is what
// turns "a digest is published" into "a new digest is published".
func ensureModuleVersion(
	ctx context.Context,
	c moduleObjectClient,
	observer moduleObserver,
	moduleName, imageTag string,
	policy moduleWaitPolicy,
) error {
	if moduleName == "" {
		return errors.New("the module name must not be empty")
	}
	if err := ValidateModuleImageTag(imageTag); err != nil {
		return fmt.Errorf("the image tag of module %q %w", moduleName, err)
	}

	before, err := observer.observeModule(ctx, moduleName)
	if err != nil {
		return fmt.Errorf("reading the state of module %q before the write: %w", moduleName, err)
	}

	configAction, err := ensureModuleConfigEnabled(ctx, c, moduleName)
	if err != nil {
		return err
	}
	overrideAction, previousTag, err := ensureModulePullOverride(ctx, c, moduleName, imageTag)
	if err != nil {
		return err
	}

	want := moduleReadiness{
		DigestBefore:          before.MPODigest,
		Digest:                moduleDigestRequirementFor(before, imageTag),
		ImageTag:              imageTag,
		ReadyTransitionBefore: before.ModuleReadyTransition,
		ImagesBefore:          observationImages(before),
	}

	fmt.Fprintf(GinkgoWriter,
		"[%s] [module] %s: ModuleConfig %s, ModulePullOverride %s (tag %q -> %q); before the write:"+
			" bundle digest %q, %s version %q in phase %q; waiting up to %s for the rollout"+
			" (bundle digest: %s, observation window %s)\n",
		time.Now().Format("15:04:05.000"), moduleName, configAction, overrideAction,
		previousTag, imageTag, before.MPODigest,
		gvkModule.Kind, before.ModuleVersion, before.ModulePhase,
		policy.Timeout, want.Digest, policy.ObservationWindow)

	return awaitModuleReady(ctx, observer, moduleName, want, policy)
}

// moduleWriteAttempts bounds how many times a create-or-update is retaken after
// another writer got there first. Two passes settle the race this helper actually
// meets — an accidental --procs>1, where every worker aims at the SAME tag, so
// the pass after the lost write finds the object already correct and writes
// nothing; the third is slack for a stand where somebody edits the object by hand
// at the same moment.
const moduleWriteAttempts = 3

// retryOnModuleWriteRace re-drives a create-or-update whose write lost a race;
// what names the write, and prefixes both the progress line and the error this
// gives up with.
//
// AlreadyExists means somebody created the object between our Get and our
// Create; Conflict means somebody updated it between our Get and our Update.
// Both are answered the same way — re-read the object and take the decision
// again — because the writer that beat us is another Ginkgo worker of this very
// suite aiming at the same tag, so the next pass converges. Neither is allowed to
// reach the caller as a failure: the pre-discovery hook runs once per worker, and
// an accidental parallel run has to slow the suite down instead of breaking it.
//
// Any other error is returned as it is: a write that failed on its own merits is
// not improved by repeating it. This retry cannot live in retryTransport either —
// that one retries transport-level failures (>=500) and has no way to re-read an
// object and rebuild the request body, which is the whole point here.
func retryOnModuleWriteRace(what string, attempt func() error) error {
	var err error
	for i := 1; i <= moduleWriteAttempts; i++ {
		if err = attempt(); err == nil {
			return nil
		}
		if !apierrors.IsAlreadyExists(err) && !apierrors.IsConflict(err) {
			return err
		}
		fmt.Fprintf(GinkgoWriter,
			"[%s] [module] %s: another writer got there first (attempt %d/%d: %v), re-reading\n",
			time.Now().Format("15:04:05.000"), what, i, moduleWriteAttempts, err)
	}
	return fmt.Errorf("%s: another writer kept winning through %d attempts: %w", what, moduleWriteAttempts, err)
}

// ensureModuleConfigEnabled makes sure the module's ModuleConfig exists and is
// enabled, touching nothing else: a stand's settings and version are its own,
// and Deckhouse ignores a ModulePullOverride of a disabled module.
//
// A write a concurrent caller won is retaken on a re-read object — see
// retryOnModuleWriteRace.
func ensureModuleConfigEnabled(
	ctx context.Context,
	c moduleObjectClient,
	moduleName string,
) (moduleObjectAction, error) {
	var action moduleObjectAction
	err := retryOnModuleWriteRace(
		fmt.Sprintf("enabling %s %q", gvkModuleConfig.Kind, moduleName),
		func() error {
			var err error
			action, err = ensureModuleConfigEnabledOnce(ctx, c, moduleName)
			return err
		})
	if err != nil {
		return "", err
	}
	return action, nil
}

// ensureModuleConfigEnabledOnce is one pass of the ModuleConfig create-or-update:
// read, decide, write. It hands AlreadyExists and Conflict to its caller rather
// than handling them, because deciding again requires the object to be read again.
func ensureModuleConfigEnabledOnce(
	ctx context.Context,
	c moduleObjectClient,
	moduleName string,
) (moduleObjectAction, error) {
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(gvkModuleConfig)
	err := c.Get(ctx, client.ObjectKey{Name: moduleName}, current)
	switch {
	case apierrors.IsNotFound(err):
		if err := c.Create(ctx, buildModuleConfig(moduleName)); err != nil {
			return "", fmt.Errorf("creating %s %q: %w", gvkModuleConfig.Kind, moduleName, err)
		}
		return moduleObjectCreated, nil
	case err != nil:
		return "", fmt.Errorf("reading %s %q: %w", gvkModuleConfig.Kind, moduleName, err)
	}

	enabled, found, err := unstructured.NestedBool(current.Object, "spec", "enabled")
	if err != nil {
		return "", fmt.Errorf("reading spec.enabled of %s %q: %w", gvkModuleConfig.Kind, moduleName, err)
	}
	if found && enabled {
		return moduleObjectUnchanged, nil
	}
	if err := unstructured.SetNestedField(current.Object, true, "spec", "enabled"); err != nil {
		return "", fmt.Errorf("setting spec.enabled of %s %q: %w", gvkModuleConfig.Kind, moduleName, err)
	}
	if err := c.Update(ctx, current); err != nil {
		return "", fmt.Errorf("enabling %s %q: %w", gvkModuleConfig.Kind, moduleName, err)
	}
	return moduleObjectUpdated, nil
}

// ensureModulePullOverride makes sure the module's ModulePullOverride pins
// imageTag, and reports what it did together with the tag that was there before.
//
// A live override is retagged in place (that retag is the upgrade), and the tag
// is the only field rewritten: rollback and scanInterval are filled in when
// absent, so an override a stand configured on purpose is not overwritten. An
// override that already pins imageTag is not written at all — that no-op is what
// makes the helper idempotent.
//
// A write a concurrent caller won is retaken on a re-read object — see
// retryOnModuleWriteRace. Note what the retry then reports: a sibling that
// created or retagged the object to the SAME tag first leaves this call with
// "unchanged", which is why the readiness criterion derives its demand from the
// pre-write snapshot and not from this action (moduleDigestRequirementFor).
func ensureModulePullOverride(
	ctx context.Context,
	c moduleObjectClient,
	moduleName, imageTag string,
) (moduleObjectAction, string, error) {
	var (
		action      moduleObjectAction
		previousTag string
	)
	err := retryOnModuleWriteRace(
		fmt.Sprintf("pinning %s %q to tag %q", gvkModulePullOverride.Kind, moduleName, imageTag),
		func() error {
			var err error
			action, previousTag, err = ensureModulePullOverrideOnce(ctx, c, moduleName, imageTag)
			return err
		})
	if err != nil {
		return "", "", err
	}
	return action, previousTag, nil
}

// ensureModulePullOverrideOnce is one pass of the ModulePullOverride
// create-or-update: read, decide, write. It hands AlreadyExists and Conflict to
// its caller rather than handling them, because deciding again requires the
// object to be read again.
func ensureModulePullOverrideOnce(
	ctx context.Context,
	c moduleObjectClient,
	moduleName, imageTag string,
) (moduleObjectAction, string, error) {
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(gvkModulePullOverride)
	err := c.Get(ctx, client.ObjectKey{Name: moduleName}, current)
	switch {
	case apierrors.IsNotFound(err):
		if err := c.Create(ctx, buildModulePullOverride(moduleName, imageTag)); err != nil {
			return "", "", fmt.Errorf("creating %s %q: %w", gvkModulePullOverride.Kind, moduleName, err)
		}
		return moduleObjectCreated, "", nil
	case err != nil:
		return "", "", fmt.Errorf("reading %s %q: %w", gvkModulePullOverride.Kind, moduleName, err)
	}

	previousTag, _, err := unstructured.NestedString(current.Object, "spec", "imageTag")
	if err != nil {
		return "", "", fmt.Errorf("reading spec.imageTag of %s %q: %w", gvkModulePullOverride.Kind, moduleName, err)
	}
	if previousTag == imageTag {
		return moduleObjectUnchanged, previousTag, nil
	}

	if err := unstructured.SetNestedField(current.Object, imageTag, "spec", "imageTag"); err != nil {
		return "", "", fmt.Errorf("setting spec.imageTag of %s %q: %w", gvkModulePullOverride.Kind, moduleName, err)
	}
	if err := fillModulePullOverrideDefaults(current); err != nil {
		return "", "", fmt.Errorf("completing %s %q: %w", gvkModulePullOverride.Kind, moduleName, err)
	}
	if err := c.Update(ctx, current); err != nil {
		return "", "", fmt.Errorf("retagging %s %q from %q to %q: %w",
			gvkModulePullOverride.Kind, moduleName, previousTag, imageTag, err)
	}
	return moduleObjectUpdated, previousTag, nil
}

// buildModuleConfig builds the ModuleConfig that enables the module. Only
// spec.enabled is set: everything else about a module's configuration belongs to
// whoever installed the stand.
func buildModuleConfig(moduleName string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"enabled": true},
	}}
	u.SetGroupVersionKind(gvkModuleConfig)
	u.SetName(moduleName)
	return u
}

// buildModulePullOverride builds the ModulePullOverride that pins the module to
// a dev build: the tag to follow, no rollback of the module's version, and a
// scan interval short enough for a retag to be picked up in seconds.
func buildModulePullOverride(moduleName, imageTag string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"imageTag":     imageTag,
			"rollback":     false,
			"scanInterval": modulePullOverrideScanInterval,
		},
	}}
	u.SetGroupVersionKind(gvkModulePullOverride)
	u.SetName(moduleName)
	return u
}

// fillModulePullOverrideDefaults adds the fields a hand-made override may lack,
// leaving present values alone.
func fillModulePullOverrideDefaults(u *unstructured.Unstructured) error {
	if _, found, err := unstructured.NestedBool(u.Object, "spec", "rollback"); err != nil {
		return fmt.Errorf("reading spec.rollback: %w", err)
	} else if !found {
		if err := unstructured.SetNestedField(u.Object, false, "spec", "rollback"); err != nil {
			return fmt.Errorf("setting spec.rollback: %w", err)
		}
	}
	interval, _, err := unstructured.NestedString(u.Object, "spec", "scanInterval")
	if err != nil {
		return fmt.Errorf("reading spec.scanInterval: %w", err)
	}
	if interval == "" {
		if err := unstructured.SetNestedField(
			u.Object, modulePullOverrideScanInterval, "spec", "scanInterval"); err != nil {
			return fmt.Errorf("setting spec.scanInterval: %w", err)
		}
	}
	return nil
}

// moduleStableSamples is how many CONSECUTIVE accepting polls span window at
// poll: the opening sample, plus enough intervals after it to cover the window.
//
// The count is derived from the two durations rather than measured against a
// clock, so the loop is deterministic: a unit test that asserts how many times
// the observer was read cannot be thrown off by a sleep that overslept, which on
// a loaded machine a millisecond-scale one always does.
func moduleStableSamples(window, poll time.Duration) int {
	if window <= 0 || poll <= 0 {
		return 1
	}
	return int((window+poll-1)/poll) + 1
}

// awaitModuleReady polls the module until the criterion accepts it and the
// accepted state holds unchanged across the observation window, the budget runs
// out, or ctx ends.
//
// A failed read is not a verdict: a module rollout restarts the very webhooks
// and API extensions the read goes through, so a read error counts as "not yet"
// and is only reported if the budget expires on it. The whole policy is a
// parameter so unit tests walk the loop without waiting in real time.
func awaitModuleReady(
	ctx context.Context,
	observer moduleObserver,
	moduleName string,
	want moduleReadiness,
	policy moduleWaitPolicy,
) error {
	required := moduleStableSamples(policy.ObservationWindow, policy.Poll)

	deadline := time.Now().Add(policy.Timeout)
	var (
		last        error
		accepted    int
		fingerprint string
	)

	for {
		obs, err := observer.observeModule(ctx, moduleName)
		if err != nil {
			last, accepted = fmt.Errorf("reading the state of module %q: %w", moduleName, err), 0
		} else if last = checkModuleReady(want, obs); last != nil {
			accepted = 0
		} else {
			if sample := moduleStateFingerprint(obs); sample != fingerprint {
				accepted, fingerprint = 0, sample
			}
			accepted++
			if accepted >= required {
				reportModuleReady(moduleName, want, obs)
				return nil
			}
			last = fmt.Errorf("the module looks rolled out, but the state held for %d of the %d polls"+
				" that span the %s observation window", accepted, required, policy.ObservationWindow)
		}

		if !time.Now().Before(deadline) {
			return fmt.Errorf("module %q did not become ready within %s: %w",
				moduleName, policy.Timeout, last)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for module %q to become ready: %w; last state: %v",
				moduleName, ctx.Err(), last)
		case <-time.After(policy.Poll):
		}
	}
}

// reportModuleReady writes the line a finished wait leaves in the log. It says
// out loud when NO pod template changed, because that is the one outcome a reader
// is entitled to doubt: it looks like the retag did nothing, and it is exactly
// what two content-addressed builds of the same component produce.
func reportModuleReady(moduleName string, want moduleReadiness, obs moduleObservation) {
	replaced := moduleImagesChanged(want.ImagesBefore, obs)
	templates := fmt.Sprintf("pod templates replaced: %v", replaced)
	if len(replaced) == 0 {
		templates = "no pod template changed, so the two builds render identical workloads"
	}
	fmt.Fprintf(GinkgoWriter,
		"[%s] [module] %s ready: tag %q, bundle digest %q, %s version %q in phase %q; %s\n",
		time.Now().Format("15:04:05.000"), moduleName, obs.MPOTag, obs.MPODigest,
		gvkModule.Kind, obs.ModuleVersion, obs.ModulePhase, templates)
}
