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
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// defaultIOImage is the image of the writer pod. busybox is what the
	// repository already runs a pod on a PVC with
	// (e2e/linstor-migrator/tests/helpers/resources.go); the program needs
	// nothing beyond a POSIX shell, sha256sum, date and the usual text tools.
	defaultIOImage = "busybox:latest"

	// EnvUpgradeImage overrides defaultIOImage. A stand without access to Docker
	// Hub (or without the image cached on its nodes) MUST set it to a
	// busybox-compatible image it can pull, otherwise every writer pod ends up in
	// ImagePullBackOff.
	EnvUpgradeImage = "E2E_UPGRADE_IMAGE"

	// DefaultVolumeSize is the size of the workload's PersistentVolumeClaim when
	// the caller passes none. It is also the documented default of
	// E2E_UPGRADE_VOLUME_SIZE, so the literal lives here only once.
	DefaultVolumeSize = "1Gi"

	// podIOContainerName and podIOVolumeName name the single container of the
	// writer pod and the volume it mounts.
	podIOContainerName = "io"
	podIOVolumeName    = "data"

	// podIOPVCSuffix and podIOPodSuffix turn the workload name into the names of
	// the two objects, so a `kubectl get pvc,pod` tells at a glance which pod
	// writes to which claim.
	podIOPVCSuffix = "-pvc"
	podIOPodSuffix = "-pod"

	// Defaults of PodIOWorkloadOptions. The beat interval is whole seconds
	// because BusyBox has neither a fractional sleep nor a sub-second date
	// format; against a freeze tolerance measured in tens of seconds, one beat
	// per second is precise enough.
	podIODefaultInterval       = time.Second
	podIODefaultDataKiB        = 64
	podIODefaultRunningTimeout = 5 * time.Minute
	podIODefaultStartTimeout   = 90 * time.Second
	podIODefaultStopTimeout    = 60 * time.Second
	podIODefaultPoll           = 2 * time.Second

	// podIOTerminationGrace is short on purpose: the writer holds no state that
	// needs unwinding — everything it produced is already fsync'ed on the volume.
	podIOTerminationGrace int64 = 5
)

// podIONameRe keeps a workload name to a single DNS-1123 label, because it is
// used as the prefix of two object names.
var podIONameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// podIOMaxNameLen leaves room for the longest object suffix in the 63-character
// budget of a DNS-1123 label.
const podIOMaxNameLen = 63 - len(podIOPVCSuffix)

// PodIOWorkloadOptions configures a pod that writes to a PersistentVolumeClaim.
type PodIOWorkloadOptions struct {
	// Namespace is where the claim and the pod are created. Required — and it
	// SHOULD be a namespace the framework owns (f.TestNS), whose deletion
	// cascades over both objects.
	Namespace string

	// StorageClassName is the storage class the claim asks for. Required. A
	// ReplicatedStorageClass creates a StorageClass of the same name, so this is
	// the RSC's name in the upgrade suite.
	StorageClassName string

	// Name is the prefix of the claim ("<Name>-pvc") and of the pod
	// ("<Name>-pod"). It defaults to a name unique to THIS call, so several
	// workloads started by one spec never collide. Pass a name from the
	// framework (f.UniqueName("io0")) — never a random or timestamped one, or a
	// leftover object could not be recognized on the next run.
	Name string

	// Size is the claim's size as a Kubernetes quantity ("1Gi"). Defaults to
	// DefaultVolumeSize.
	Size string

	// Image is the writer image. Defaults to EnvUpgradeImage, or to
	// defaultIOImage when that variable is unset.
	Image string

	// Interval is the pause between two verified writes, rounded to whole
	// seconds with a floor of one second (see program).
	Interval time.Duration

	// MaxHeartbeatGap is the longest tolerated distance between two consecutive
	// verified writes. It bounds gaps HISTORICALLY: a freeze longer than this
	// fails the workload even when writes resumed before anyone looked.
	MaxHeartbeatGap time.Duration

	// DataKiB is the size of the one-shot data file whose sha256 is recorded at
	// creation and re-verified later. Defaults to podIODefaultDataKiB.
	DataKiB int

	// RunningTimeout bounds the wait for the pod to run (it covers the image
	// pull and, with WaitForFirstConsumer, the provisioning of the volume),
	// StartTimeout the wait for the first verified write, and StopTimeout the
	// wait for the writer to acknowledge a stop.
	RunningTimeout time.Duration
	StartTimeout   time.Duration
	StopTimeout    time.Duration
}

// PodIOFreeze is one interval in which the writer published no beat, i.e. a
// window in which nothing reached the volume.
type PodIOFreeze struct {
	// Duration is the distance between the two beats that bound the freeze.
	Duration time.Duration
	// From and To are those beats' timestamps, on the pod's own clock.
	From time.Time
	To   time.Time
	// FromSequence and ToSequence are their sequence numbers, which is what
	// makes a freeze findable in the journal.
	FromSequence int64
	ToSequence   int64
}

// String renders the freeze for failure messages.
func (f PodIOFreeze) String() string {
	return fmt.Sprintf("%s between beats %d and %d (%s .. %s)",
		f.Duration.Truncate(time.Millisecond), f.FromSequence, f.ToSequence,
		f.From.Format("15:04:05"), f.To.Format("15:04:05"))
}

// PodIOWorkloadStatus is a point-in-time view of the writer, computed from one
// read of the pod object plus one probe inside it.
type PodIOWorkloadStatus struct {
	// Pod is the state of the writer pod itself.
	Pod PodIOPodState

	// LastSequence is the sequence number of the last verified write in the
	// observed journal tail, or -1 when it holds none.
	LastSequence int64
	LastWriteAt  time.Time

	// Gap is the age of the last verified write on the POD's clock, and Stalled
	// says it exceeds MaxHeartbeatGap while the writer has not terminated.
	Gap     time.Duration
	Stalled bool

	// MaxObservedGap is the largest distance between two consecutive verified
	// writes in the observed journal, and GapExceeded says it went over
	// MaxHeartbeatGap. Unlike Gap it is historical evidence: a freeze that
	// already ended still shows here as long as its boundary is inside the
	// observed tail — and always at the final, whole-journal read.
	MaxObservedGap time.Duration
	GapExceeded    bool

	// Terminated is set once the writer wrote its last record: a clean stop, or
	// a failure of the data path.
	Terminated *IOWorkloadTermination

	journal ioJournal
}

// String renders the status for failure messages and logs.
func (s PodIOWorkloadStatus) String() string {
	parts := []string{
		fmt.Sprintf("pod=%s", s.Pod),
		fmt.Sprintf("lastSequence=%d", s.LastSequence),
	}
	if !s.LastWriteAt.IsZero() {
		parts = append(parts, fmt.Sprintf("gap=%s", s.Gap.Truncate(time.Millisecond)))
	}
	if s.Stalled {
		parts = append(parts, "stalled")
	}
	if s.GapExceeded {
		parts = append(parts, fmt.Sprintf("stalled-for=%s", s.MaxObservedGap.Truncate(time.Millisecond)))
	}
	if s.Terminated != nil {
		parts = append(parts, fmt.Sprintf("terminated(failed=%t)=%q", s.Terminated.Failed, s.Terminated.Message))
	}
	return strings.Join(parts, " ")
}

// PodIOPodState is the writer pod's state, projected from the pod object.
type PodIOPodState struct {
	// Found reports whether the pod exists at all.
	Found bool
	Phase corev1.PodPhase
	// NodeName is where the pod was scheduled ("" until it was).
	NodeName string
	// Ready reports that the writer container is running AND ready, which is the
	// only state in which the journal can be read out of it.
	Ready bool
	// Restarts is how often the writer container was restarted. A restart is not
	// a failure by itself — the writer resumes its sequence — but it always
	// leaves a gap in the journal.
	Restarts int32
	// Note explains why Ready is false, quoting the container's waiting or
	// terminated state (an ImagePullBackOff reason, an exit code and message).
	Note string
}

// String renders the pod state for messages.
func (s PodIOPodState) String() string {
	if !s.Found {
		return "absent"
	}
	out := string(s.Phase)
	if s.NodeName != "" {
		out += " on " + s.NodeName
	}
	if s.Restarts > 0 {
		out += fmt.Sprintf(" restarts=%d", s.Restarts)
	}
	if !s.Ready {
		out += " not-ready"
	}
	if s.Note != "" {
		out += " (" + s.Note + ")"
	}
	return out
}

// PodIOWorkload is a pod writing continuously to a PersistentVolumeClaim. It
// proves that a volume keeps accepting I/O — through the whole CSI path a real
// consumer uses — and it carries the data whose checksum proves that nothing was
// lost on the way.
//
// Obtain one from Framework.StartPodIOWorkload.
type PodIOWorkload struct {
	f    *Framework
	cl   podIOClient
	opts PodIOWorkloadOptions

	pvcName    string
	podName    string
	image      string
	size       resource.Quantity
	volumeName string

	poll      time.Duration
	started   bool
	cleanedUp bool
}

// podIOClient is the seam the workload's object access goes through.
// client.Client implements it against the cluster; helper unit tests substitute
// the controller-runtime fake client, so create-or-adopt and deletion are
// exercised — with real NotFound semantics — without a cluster.
type podIOClient interface {
	Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error
	Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
	Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
}

// ---------------------------------------------------------------------------
// Exported helpers
// ---------------------------------------------------------------------------

// StartPodIOWorkload creates a PersistentVolumeClaim and a pod writing to it,
// and returns once that pod completed its first verified write.
//
// The calling spec MUST carry LabelDisruptive, on itself or on an enclosing
// container. The requirement is enforced, not merely stated: the helper spawns a
// pod that keeps writing to a volume of a SHARED stand and — started from a
// BeforeAll, which is how the upgrade suite uses it — keeps running across every
// spec of that container, holding its claim, its attachment and its share of the
// pool until the container's cleanup. RequireDisruptiveSpec refuses the call
// before any object is created.
//
// Guarantees:
//   - The claim asks for opts.Size of opts.StorageClassName with
//     volumeMode=Filesystem and accessModes=[ReadWriteOnce]; the pod mounts it at
//     /data and its image is pulled with imagePullPolicy=IfNotPresent — the tag
//     of the default image is `latest`, whose implicit Always would make every
//     pod of the suite depend on the registry although the node's cached image
//     would do.
//   - Both objects are created create-or-adopt, so a second call with the same
//     name adopts what the first one left instead of failing.
//   - The wait for the pod ends in a message that names the image, the pod's
//     phase and the container's waiting/terminated reason, plus the
//     E2E_UPGRADE_IMAGE hint — an unreachable registry is the first thing a new
//     stand runs into and it must not look like a framework defect.
//   - Once the pod runs, VolumeName reports the claim's spec.volumeName, which
//     is the name of the ReplicatedVolume behind it (the CSI driver names the RV
//     after the CSI volume, and the provisioner names the PV the same).
//   - The writer keeps a journal ON the volume and a data file whose sha256 was
//     recorded when it was written; Observe/AwaitProgress read the journal,
//     AnalyzeFreezes measures the gaps in it, and VerifyChecksum re-hashes the
//     data file inside the very same pod.
//   - Cleanup is registered BEFORE the first object is created and is
//     idempotent: it stops the writer, reads the WHOLE journal, fails the run if
//     the writer never wrote, failed, or stalled longer than
//     opts.MaxHeartbeatGap, and only then deletes the pod and the claim. Because
//     Ginkgo runs cleanups in reverse order, it always runs before the teardown
//     of the namespace or the storage class registered before it. Where that
//     cleanup lands is decided by the CALLER: registered from a BeforeAll it
//     becomes a CleanupAfterAll and the writer lives through every spec of the
//     container, registered from an It it dies with that spec.
//
// The pod is NOT deleted when the run fails — the state of the stand is the
// diagnosis material — beyond what the cleanup above does.
func (f *Framework) StartPodIOWorkload(ctx context.Context, opts PodIOWorkloadOptions) *PodIOWorkload {
	GinkgoHelper()
	// Guarded before validation and before defaulting, so the refused operation
	// is named from the options exactly as the caller passed them. Quoted rather
	// than interpolated bare: a field left empty has to be visible as "".
	RequireDisruptiveSpec(fmt.Sprintf(
		"running a continuous I/O writer pod on a new volume of storage class %q in namespace %q",
		opts.StorageClassName, opts.Namespace))

	w, err := f.newPodIOWorkload(f.Client, opts)
	if err != nil {
		Fail(fmt.Sprintf("pod io workload: %v", err))
	}

	// Registered before anything exists: no failure path can leave the claim or
	// the pod behind.
	DeferCleanup(func(ctx SpecContext) { w.Cleanup(ctx) })

	if err := w.start(ctx); err != nil {
		Fail(fmt.Sprintf("pod io workload %q in namespace %q: %v", w.podName, w.opts.Namespace, err))
	}
	return w
}

// Namespace is where the claim and the pod live.
func (w *PodIOWorkload) Namespace() string { return w.opts.Namespace }

// PVCName is the name of the workload's PersistentVolumeClaim.
func (w *PodIOWorkload) PVCName() string { return w.pvcName }

// PodName is the name of the writer pod.
func (w *PodIOWorkload) PodName() string { return w.podName }

// Image is the image the writer pod runs, after the E2E_UPGRADE_IMAGE override.
func (w *PodIOWorkload) Image() string { return w.image }

// VolumeName is spec.volumeName of the bound claim — the name of the PV and of
// the ReplicatedVolume behind it. Set once the pod runs, empty before that.
func (w *PodIOWorkload) VolumeName() string { return w.volumeName }

// JournalPath is the beat journal inside the pod, useful in failure reports.
func (w *PodIOWorkload) JournalPath() string { return w.journalPath() }

// Observe returns the current status of the writer, read from the pod object and
// from the tail of the journal.
func (w *PodIOWorkload) Observe(ctx context.Context) PodIOWorkloadStatus {
	GinkgoHelper()
	st, err := w.observe(ctx)
	if err != nil {
		Fail(w.failure(err))
	}
	return st
}

// AwaitProgress blocks until the writer completed minWrites more verified writes
// than it had at the moment of the call, and returns the status that satisfied
// it. It fails the spec when the writer fails, when it stalls beyond
// MaxHeartbeatGap, or when it makes no such progress within StartTimeout.
func (w *PodIOWorkload) AwaitProgress(ctx context.Context, minWrites int64) PodIOWorkloadStatus {
	GinkgoHelper()
	st, err := w.awaitProgress(ctx, minWrites)
	if err != nil {
		Fail(w.failure(err))
	}
	return st
}

// AnalyzeFreezes reads the WHOLE journal — not the tail Observe uses — and
// reports the largest gap between two consecutive verified writes plus every gap
// longer than MaxHeartbeatGap.
//
// The whole journal is the point: a freeze is only historical evidence for as
// long as its boundary is inside what was read, so a tail can miss a stall that
// happened minutes ago while the writer is happily beating now.
func (w *PodIOWorkload) AnalyzeFreezes(ctx context.Context) (time.Duration, []PodIOFreeze) {
	GinkgoHelper()
	maxGap, freezes, err := w.analyzeFreezes(ctx)
	if err != nil {
		Fail(w.failure(err))
	}
	return maxGap, freezes
}

// VerifyChecksum re-hashes the data file in the writer pod and compares it with
// the digest recorded when the file was written.
//
// The re-read happens in the SAME pod on purpose: the claim is ReadWriteOnce, so
// a second pod on another node could not mount the volume at all — and the
// writer's own pod is alive by definition as long as the workload is (any
// progress wait would have failed otherwise).
func (w *PodIOWorkload) VerifyChecksum(ctx context.Context) {
	GinkgoHelper()
	if err := w.verifyChecksum(ctx); err != nil {
		Fail(w.failure(err))
	}
}

// VerifyVolumeHandle asserts that the volume the claim was bound to is the volume
// the CSI driver created for it: PV.spec.csi.volumeHandle must be the PV's own
// name, which is what makes VolumeName usable as the ReplicatedVolume's name.
//
// The check exists so that a change in the driver's naming scheme is caught here,
// with a message about naming, instead of surfacing as a ReplicatedVolume that
// does not exist. Read-only, idempotent.
func (w *PodIOWorkload) VerifyVolumeHandle(ctx context.Context) {
	GinkgoHelper()
	if err := w.verifyVolumeHandle(ctx); err != nil {
		Fail(w.failure(err))
	}
}

// Stop asks the writer to finish and waits for its last journal record. The pod
// stays alive (idle) so the journal and the data file remain readable. It is
// idempotent.
func (w *PodIOWorkload) Stop(ctx context.Context) {
	GinkgoHelper()
	if err := w.stop(ctx); err != nil {
		Fail(w.failure(err))
	}
}

// Cleanup stops the writer, checks the whole journal and deletes the pod and the
// claim. It is registered automatically by StartPodIOWorkload and is idempotent,
// so calling it explicitly is allowed.
func (w *PodIOWorkload) Cleanup(ctx context.Context) {
	GinkgoHelper()
	if err := w.cleanup(ctx); err != nil {
		Fail(fmt.Sprintf("pod io workload %q cleanup in namespace %q: %v", w.podName, w.opts.Namespace, err))
	}
}

// ---------------------------------------------------------------------------
// Core: everything below returns errors so it can be unit-tested with a stub
// runner and a fake client, without a cluster.
// ---------------------------------------------------------------------------

// failure renders err the way every exported wrapper reports it.
func (w *PodIOWorkload) failure(err error) string {
	return fmt.Sprintf("pod io workload %q in namespace %q: %v", w.podName, w.opts.Namespace, err)
}

// newPodIOWorkload validates the options, applies the defaults and derives the
// object names.
func (f *Framework) newPodIOWorkload(cl podIOClient, opts PodIOWorkloadOptions) (*PodIOWorkload, error) {
	if opts.Name == "" {
		// One name per call, not per spec: a suffixed UniqueName is stable within
		// a spec, so the second workload of a spec creating N of them would
		// address the first one's objects.
		opts.Name = f.UniqueName() + "-io"
	}

	switch {
	case opts.Namespace == "":
		return nil, errors.New("require: Namespace must not be empty")
	case opts.StorageClassName == "":
		return nil, errors.New("require: StorageClassName must not be empty")
	case !podIONameRe.MatchString(opts.Name):
		return nil, fmt.Errorf("require: Name %q must match %s", opts.Name, podIONameRe)
	case len(opts.Name) > podIOMaxNameLen:
		return nil, fmt.Errorf("require: Name %q is %d characters, at most %d fit an object name",
			opts.Name, len(opts.Name), podIOMaxNameLen)
	}

	if opts.Size == "" {
		opts.Size = DefaultVolumeSize
	}
	if opts.Image == "" {
		opts.Image = envOrDefault(EnvUpgradeImage, defaultIOImage)
	}
	if opts.Interval == 0 {
		opts.Interval = podIODefaultInterval
	}
	if opts.MaxHeartbeatGap == 0 {
		// The same tolerance the node-level writer defaults to, so a freeze means
		// the same thing whichever writer reported it.
		opts.MaxHeartbeatGap = ioWorkloadDefaultMaxGap
	}
	if opts.DataKiB == 0 {
		opts.DataKiB = podIODefaultDataKiB
	}
	if opts.RunningTimeout == 0 {
		opts.RunningTimeout = podIODefaultRunningTimeout
	}
	if opts.StartTimeout == 0 {
		opts.StartTimeout = podIODefaultStartTimeout
	}
	if opts.StopTimeout == 0 {
		opts.StopTimeout = podIODefaultStopTimeout
	}

	size, err := resource.ParseQuantity(opts.Size)
	if err != nil {
		return nil, fmt.Errorf("require: Size %q is not a Kubernetes quantity: %w", opts.Size, err)
	}
	switch {
	case size.Sign() <= 0:
		return nil, fmt.Errorf("require: Size %q must be positive", opts.Size)
	case opts.Interval <= 0:
		return nil, fmt.Errorf("require: Interval must be positive, got %s", opts.Interval)
	case opts.MaxHeartbeatGap <= 0:
		return nil, fmt.Errorf("require: MaxHeartbeatGap must be positive, got %s", opts.MaxHeartbeatGap)
	case opts.DataKiB < 1:
		return nil, fmt.Errorf("require: DataKiB must be at least 1, got %d", opts.DataKiB)
	}

	return &PodIOWorkload{
		f:       f,
		cl:      cl,
		opts:    opts,
		pvcName: opts.Name + podIOPVCSuffix,
		podName: opts.Name + podIOPodSuffix,
		image:   opts.Image,
		size:    size,
		poll:    podIODefaultPoll,
	}, nil
}

// start creates the two objects and returns once the writer proved the data path
// works: the pod runs, the claim is bound, and the journal holds a verified
// write.
func (w *PodIOWorkload) start(ctx context.Context) error {
	if err := w.ensurePVC(ctx); err != nil {
		return err
	}
	if err := w.ensurePod(ctx); err != nil {
		return err
	}
	if err := w.awaitPodRunning(ctx); err != nil {
		return err
	}
	if err := w.awaitVolumeName(ctx); err != nil {
		return err
	}

	st, err := w.awaitFirstBeat(ctx)
	if err != nil {
		return err
	}
	w.started = true

	fmt.Fprintf(GinkgoWriter, "[%s] [pod-io-workload] pod=%s/%s pvc=%s volume=%s image=%s: %s\n",
		time.Now().Format("15:04:05.000"), w.opts.Namespace, w.podName, w.pvcName, w.volumeName, w.image, st)
	return nil
}

// ensurePVC creates the claim, or adopts one that is already there.
func (w *PodIOWorkload) ensurePVC(ctx context.Context) error {
	return w.ensureObject(ctx, "PersistentVolumeClaim", w.pvcName,
		&corev1.PersistentVolumeClaim{}, func() client.Object { return w.buildPVC() })
}

// ensurePod creates the writer pod, or adopts one that is already there.
func (w *PodIOWorkload) ensurePod(ctx context.Context) error {
	return w.ensureObject(ctx, "Pod", w.podName,
		&corev1.Pod{}, func() client.Object { return w.buildPod() })
}

// ensureObject is create-or-adopt for one object: an object that already carries
// this workload's name belongs to an earlier call of this very workload (the name
// is unique per call), so it is adopted instead of being an error. AlreadyExists
// is treated the same way — the read may have come from a cache that had not seen
// the object yet.
func (w *PodIOWorkload) ensureObject(
	ctx context.Context,
	kind, name string,
	into client.Object,
	build func() client.Object,
) error {
	key := client.ObjectKey{Namespace: w.opts.Namespace, Name: name}
	err := w.cl.Get(ctx, key, into)
	switch {
	case err == nil:
		fmt.Fprintf(GinkgoWriter, "[%s] [pod-io-workload] adopting the existing %s %s/%s\n",
			time.Now().Format("15:04:05.000"), kind, w.opts.Namespace, name)
		return nil
	case !apierrors.IsNotFound(err):
		return fmt.Errorf("reading %s %s/%s: %w", kind, w.opts.Namespace, name, err)
	}

	if err := w.cl.Create(ctx, build()); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("creating %s %s/%s: %w", kind, w.opts.Namespace, name, err)
	}
	return nil
}

// buildPVC builds the workload's claim.
func (w *PodIOWorkload) buildPVC() *corev1.PersistentVolumeClaim {
	volumeMode := corev1.PersistentVolumeFilesystem
	storageClass := w.opts.StorageClassName
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      w.pvcName,
			Namespace: w.opts.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			VolumeMode:       &volumeMode,
			StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: w.size},
			},
		},
	}
	w.f.stampMetadata(pvc)
	return pvc
}

// buildPod builds the writer pod.
func (w *PodIOWorkload) buildPod() *corev1.Pod {
	grace := podIOTerminationGrace
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      w.podName,
			Namespace: w.opts.Namespace,
		},
		Spec: corev1.PodSpec{
			// The writer never exits on its own: it idles after a stop and after a
			// failure, so its journal stays readable. Always is what covers the
			// case it does die anyway — the writer resumes its sequence from the
			// journal, and the restart shows up as the gap it is.
			RestartPolicy:                 corev1.RestartPolicyAlways,
			TerminationGracePeriodSeconds: &grace,
			Containers: []corev1.Container{{
				Name:  podIOContainerName,
				Image: w.image,
				// The default image is tagged `latest`, for which Kubernetes
				// implies Always — every pod would then depend on the registry
				// even where the node already has the image.
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"sh", "-c", w.program()},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      podIOVolumeName,
					MountPath: podIOMountPath,
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: podIOVolumeName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: w.pvcName,
					},
				},
			}},
		},
	}
	w.f.stampMetadata(pod)
	return pod
}

// podState reads the writer pod and projects the state the waits need.
func (w *PodIOWorkload) podState(ctx context.Context) (PodIOPodState, error) {
	var pod corev1.Pod
	err := w.cl.Get(ctx, client.ObjectKey{Namespace: w.opts.Namespace, Name: w.podName}, &pod)
	switch {
	case apierrors.IsNotFound(err):
		return PodIOPodState{}, nil
	case err != nil:
		return PodIOPodState{}, fmt.Errorf("reading Pod %s/%s: %w", w.opts.Namespace, w.podName, err)
	}
	return projectPodIOPod(&pod), nil
}

// projectPodIOPod projects the pod's phase and the state of its writer
// container. The Note is the diagnosis a failing wait reports: an image that
// cannot be pulled and a container that exited both live in the container's
// status, not in the phase.
func projectPodIOPod(pod *corev1.Pod) PodIOPodState {
	st := PodIOPodState{
		Found:    true,
		Phase:    pod.Status.Phase,
		NodeName: pod.Spec.NodeName,
	}

	var cs *corev1.ContainerStatus
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == podIOContainerName {
			cs = &pod.Status.ContainerStatuses[i]
			break
		}
	}
	if cs == nil {
		st.Note = "container " + podIOContainerName + " has published no status yet"
		return st
	}

	st.Restarts = cs.RestartCount
	st.Ready = cs.Ready && cs.State.Running != nil

	switch {
	case cs.State.Waiting != nil:
		st.Note = "waiting: " + describeContainerReason(cs.State.Waiting.Reason, cs.State.Waiting.Message)
	case cs.State.Terminated != nil:
		st.Note = fmt.Sprintf("terminated with exit code %d: %s", cs.State.Terminated.ExitCode,
			describeContainerReason(cs.State.Terminated.Reason, cs.State.Terminated.Message))
	case !cs.Ready:
		st.Note = "running but not ready"
	}

	if cs.LastTerminationState.Terminated != nil {
		st.Note += fmt.Sprintf("; previously terminated with exit code %d: %s",
			cs.LastTerminationState.Terminated.ExitCode,
			describeContainerReason(cs.LastTerminationState.Terminated.Reason,
				cs.LastTerminationState.Terminated.Message))
	}
	return st
}

// describeContainerReason joins a container state's reason and message, either of
// which Kubernetes may leave empty.
func describeContainerReason(reason, message string) string {
	switch {
	case reason == "" && message == "":
		return "no reason reported"
	case message == "":
		return reason
	case reason == "":
		return message
	}
	return reason + ": " + message
}

// awaitPodRunning waits for the writer container to run and be ready.
func (w *PodIOWorkload) awaitPodRunning(ctx context.Context) error {
	var last PodIOPodState
	err := w.pollUntil(ctx, w.opts.RunningTimeout, "the writer pod to be running", func() (bool, error) {
		st, err := w.podState(ctx)
		if err != nil {
			return false, err
		}
		last = st
		return st.Ready, nil
	})
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w; pod %s/%s (image %q) is %s. If this stand cannot pull that image,"+
		" set %s to a busybox-compatible image (sh, sha256sum, date) it can pull",
		err, w.opts.Namespace, w.podName, w.image, last, EnvUpgradeImage)
}

// awaitVolumeName waits for the claim to be bound and records the volume name,
// which is the name of the ReplicatedVolume behind it.
//
// It runs AFTER the pod is up on purpose: with a storage class in
// WaitForFirstConsumer mode the claim binds only once a consumer was scheduled,
// so waiting for the binding first would deadlock.
func (w *PodIOWorkload) awaitVolumeName(ctx context.Context) error {
	var phase corev1.PersistentVolumeClaimPhase
	err := w.pollUntil(ctx, w.opts.StartTimeout, "the claim to be bound", func() (bool, error) {
		var pvc corev1.PersistentVolumeClaim
		key := client.ObjectKey{Namespace: w.opts.Namespace, Name: w.pvcName}
		if err := w.cl.Get(ctx, key, &pvc); err != nil {
			return false, fmt.Errorf("reading PersistentVolumeClaim %s/%s: %w", w.opts.Namespace, w.pvcName, err)
		}
		phase = pvc.Status.Phase
		if pvc.Spec.VolumeName == "" {
			return false, nil
		}
		w.volumeName = pvc.Spec.VolumeName
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("%w; PersistentVolumeClaim %s/%s is %q and names no volume yet",
			err, w.opts.Namespace, w.pvcName, phase)
	}
	return nil
}

// verifyVolumeHandle reads the PersistentVolume the claim was bound to and
// compares the CSI volume it names with its own name.
func (w *PodIOWorkload) verifyVolumeHandle(ctx context.Context) error {
	if w.volumeName == "" {
		return errors.New("the claim names no volume yet, so there is nothing to verify")
	}

	var pv corev1.PersistentVolume
	if err := w.cl.Get(ctx, client.ObjectKey{Name: w.volumeName}, &pv); err != nil {
		return fmt.Errorf("reading PersistentVolume %q of claim %s/%s: %w",
			w.volumeName, w.opts.Namespace, w.pvcName, err)
	}

	switch {
	case pv.Spec.CSI == nil:
		return fmt.Errorf("PersistentVolume %q is not a CSI volume, so the claim is not backed by this module",
			w.volumeName)
	case pv.Spec.CSI.VolumeHandle != w.volumeName:
		return fmt.Errorf("PersistentVolume %q carries CSI volume handle %q: the driver no longer names the volume"+
			" after the claim's spec.volumeName, so that name is not the ReplicatedVolume's name either",
			w.volumeName, pv.Spec.CSI.VolumeHandle)
	}
	return nil
}

// awaitFirstBeat waits for the first verified write.
func (w *PodIOWorkload) awaitFirstBeat(ctx context.Context) (PodIOWorkloadStatus, error) {
	return w.await(ctx, w.opts.StartTimeout, "the first verified write",
		func(st PodIOWorkloadStatus) (bool, error) {
			if st.Terminated != nil && st.Terminated.Failed {
				return false, fmt.Errorf("the writer failed before its first verified write: %s",
					st.Terminated.Message)
			}
			return st.LastSequence >= 0, nil
		})
}

// observe runs one observation over the regular journal tail.
func (w *PodIOWorkload) observe(ctx context.Context) (PodIOWorkloadStatus, error) {
	return w.observeTail(ctx, ioWorkloadJournalTail)
}

// observeTail reads the pod object and probes the given journal tail inside the
// pod, and turns both into a status.
func (w *PodIOWorkload) observeTail(ctx context.Context, tailLines int) (PodIOWorkloadStatus, error) {
	pod, err := w.podState(ctx)
	if err != nil {
		return PodIOWorkloadStatus{}, err
	}

	res, err := w.f.runner().PodRun(ctx, w.opts.Namespace, w.podName, podIOContainerName,
		w.probeCommand(tailLines), "pod-io-workload probe "+w.podName)
	if err != nil {
		return PodIOWorkloadStatus{}, fmt.Errorf("probing the writer (pod is %s): %w", pod, err)
	}
	if res.ExitCode != 0 {
		return PodIOWorkloadStatus{}, fmt.Errorf("probing the writer exited with code %d (pod is %s): %s",
			res.ExitCode, pod, strings.TrimSpace(res.Stderr))
	}

	probe, err := parseIOProbe(res.Stdout)
	if err != nil {
		return PodIOWorkloadStatus{}, err
	}
	return w.statusFrom(pod, probe), nil
}

// statusFrom derives the status from the pod state and one probe. Every duration
// in it is computed from timestamps of the POD's clock only — the runner's clock
// never enters the comparison, so a skew between the two cannot be mistaken for
// a freeze.
func (w *PodIOWorkload) statusFrom(pod PodIOPodState, p ioProbe) PodIOWorkloadStatus {
	st := PodIOWorkloadStatus{Pod: pod, LastSequence: -1, journal: p.Journal}

	if beat := p.Journal.last(); beat != nil {
		st.LastSequence = beat.Sequence
		st.LastWriteAt = beat.At
		st.Gap = p.Now.Sub(beat.At)
		st.Stalled = p.Journal.Termination == nil && st.Gap > w.opts.MaxHeartbeatGap
	}
	if gap, endedBy := p.Journal.maxInterBeatGap(); endedBy != nil {
		st.MaxObservedGap = gap
		st.GapExceeded = gap > w.opts.MaxHeartbeatGap
	}
	if t := p.Journal.Termination; t != nil {
		st.Terminated = &IOWorkloadTermination{Failed: t.Failed, At: t.At, Message: t.Message}
	}
	return st
}

// awaitProgress waits for minWrites more verified writes.
func (w *PodIOWorkload) awaitProgress(ctx context.Context, minWrites int64) (PodIOWorkloadStatus, error) {
	from, err := w.observe(ctx)
	if err != nil {
		return PodIOWorkloadStatus{}, err
	}
	target := from.LastSequence + minWrites

	return w.await(ctx, w.opts.StartTimeout,
		fmt.Sprintf("%d more verified writes (sequence %d)", minWrites, target),
		func(st PodIOWorkloadStatus) (bool, error) {
			if st.Terminated != nil && st.Terminated.Failed {
				return false, fmt.Errorf("the writer failed: %s", st.Terminated.Message)
			}
			// A freeze visible in the journal is final evidence: the writer
			// stopped for longer than tolerated, and later progress does not
			// undo it.
			if st.GapExceeded {
				return false, fmt.Errorf("the writer stalled for %s (tolerated max %s): %s",
					st.MaxObservedGap.Truncate(time.Millisecond), w.opts.MaxHeartbeatGap, st)
			}
			return st.LastSequence >= target, nil
		})
}

// await polls the writer until done is satisfied, the writer breaks the
// expectation, or the budget runs out.
func (w *PodIOWorkload) await(
	ctx context.Context,
	timeout time.Duration,
	what string,
	done func(PodIOWorkloadStatus) (bool, error),
) (PodIOWorkloadStatus, error) {
	var last PodIOWorkloadStatus

	err := w.pollUntil(ctx, timeout, what, func() (bool, error) {
		st, err := w.observe(ctx)
		if err != nil {
			return false, err
		}
		last = st
		return done(st)
	})
	if err != nil {
		return last, fmt.Errorf("%w; last status: %s", err, last)
	}
	return last, nil
}

// pollUntil re-runs check every poll interval until it reports done, it returns
// an error, or timeout expires. The timeout error names what was waited for; the
// caller appends the observation that goes with it.
func (w *PodIOWorkload) pollUntil(
	ctx context.Context,
	timeout time.Duration,
	what string,
	check func() (bool, error),
) error {
	deadline := time.Now().Add(timeout)

	for {
		done, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out after %s waiting for %s", timeout, what)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", what, ctx.Err())
		case <-time.After(w.poll):
		}
	}
}

// analyzeFreezes reads the whole journal and measures its gaps.
func (w *PodIOWorkload) analyzeFreezes(ctx context.Context) (time.Duration, []PodIOFreeze, error) {
	st, err := w.observeTail(ctx, ioWorkloadJournalFull)
	if err != nil {
		return 0, nil, err
	}
	return st.MaxObservedGap, freezesOver(st.journal, w.opts.MaxHeartbeatGap), nil
}

// freezesOver lists every gap between two consecutive verified writes that is
// longer than threshold.
func freezesOver(j ioJournal, threshold time.Duration) []PodIOFreeze {
	var out []PodIOFreeze
	for i := 1; i < len(j.Beats); i++ {
		prev, cur := j.Beats[i-1], j.Beats[i]
		gap := cur.At.Sub(prev.At)
		if gap <= threshold {
			continue
		}
		out = append(out, PodIOFreeze{
			Duration:     gap,
			From:         prev.At,
			To:           cur.At,
			FromSequence: prev.Sequence,
			ToSequence:   cur.Sequence,
		})
	}
	return out
}

// verifyChecksum re-hashes the data file inside the pod and compares it with the
// recorded digest.
func (w *PodIOWorkload) verifyChecksum(ctx context.Context) error {
	res, err := w.f.runner().PodRun(ctx, w.opts.Namespace, w.podName, podIOContainerName,
		w.checksumCommand(), "pod-io-workload checksum "+w.podName)
	if err != nil {
		return fmt.Errorf("re-reading the data file: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("re-reading the data file exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	sums, err := parsePodIOChecksum(res.Stdout)
	if err != nil {
		return err
	}
	switch {
	case sums.Recorded == "":
		return fmt.Errorf("the writer recorded no checksum in %s: the data file was never written completely",
			w.sumPath())
	case sums.Actual == "":
		return fmt.Errorf("the data file %s could not be read back (recorded checksum %s)",
			w.dataPath(), sums.Recorded)
	case sums.Actual != sums.Recorded:
		return fmt.Errorf("the data of volume %q is corrupted: %s now hashes to %s, but %s was recorded when"+
			" it was written (%d bytes)",
			w.volumeName, w.dataPath(), sums.Actual, sums.Recorded, sums.Size)
	}

	fmt.Fprintf(GinkgoWriter, "[%s] [pod-io-workload] pod=%s/%s data intact: %s (%d bytes)\n",
		time.Now().Format("15:04:05.000"), w.opts.Namespace, w.podName, sums.Actual, sums.Size)
	return nil
}

// stop raises the stop flag and waits for the writer's last record.
func (w *PodIOWorkload) stop(ctx context.Context) error {
	st, err := w.observe(ctx)
	if err != nil {
		return err
	}
	if st.Terminated != nil {
		return nil // idempotent: the writer already had its last word
	}

	// The only command that changes state inside the pod, and therefore the only
	// one that goes through the no-retry seam: the retrying path exists for the
	// read-only probes, whose repetition is harmless by construction.
	res, err := w.f.runner().PodRunNoRetry(ctx, w.opts.Namespace, w.podName, podIOContainerName,
		w.stopCommand(), "pod-io-workload stop "+w.podName)
	if err != nil {
		return fmt.Errorf("asking the writer to stop: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("asking the writer to stop exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	_, err = w.await(ctx, w.opts.StopTimeout, "the writer to stop",
		func(st PodIOWorkloadStatus) (bool, error) {
			return st.Terminated != nil, nil
		})
	return err
}

// cleanup stops the writer, judges the whole journal while the pod can still be
// read, and only then deletes the pod and the claim.
//
// It is idempotent, and it never invents a verdict out of missing evidence: a
// workload whose start never finished has no journal to judge, and saying
// anything about it would only bury the failure that stopped the start.
func (w *PodIOWorkload) cleanup(ctx context.Context) error {
	if w.cleanedUp {
		return nil
	}
	w.cleanedUp = true

	var stopErr, obsErr, verifyErr error
	if w.started {
		pod, podErr := w.podState(ctx)
		switch {
		case podErr != nil:
			obsErr = podErr
		case !pod.Ready:
			// The writer ran once, so its journal exists — but it is inside a
			// container that is no longer running, and no exec can reach it. That
			// is a verdict of its own: reporting nothing here would turn a writer
			// that died mid-run into a green result.
			obsErr = fmt.Errorf("the writer pod %s/%s is %s, so its journal could not be read at cleanup",
				w.opts.Namespace, w.podName, pod)
		default:
			stopErr = w.stop(ctx)
			var final PodIOWorkloadStatus
			final, obsErr = w.observeTail(ctx, ioWorkloadJournalFull)
			if obsErr == nil {
				verifyErr = w.verifyFinal(final)
			}
		}
	}

	return errors.Join(stopErr, obsErr, verifyErr, w.deleteObjects(ctx))
}

// verifyFinal is the last continuity check over the whole journal: the writer
// must have written something, must not have ended on a failure of the data
// path, and must not have stalled longer than tolerated at any point of the run.
func (w *PodIOWorkload) verifyFinal(st PodIOWorkloadStatus) error {
	switch {
	case st.Terminated != nil && st.Terminated.Failed:
		return fmt.Errorf("the writer failed: %s (journal: %s in pod %s/%s)",
			st.Terminated.Message, w.journalPath(), w.opts.Namespace, w.podName)
	case st.LastSequence < 0:
		return fmt.Errorf("the writer completed no verified write (journal: %s in pod %s/%s)",
			w.journalPath(), w.opts.Namespace, w.podName)
	case st.GapExceeded:
		return fmt.Errorf("the writer stalled for %s during the run (tolerated max %s); freezes: %v",
			st.MaxObservedGap.Truncate(time.Millisecond), w.opts.MaxHeartbeatGap,
			freezesOver(st.journal, w.opts.MaxHeartbeatGap))
	}
	return nil
}

// deleteObjects removes the pod and then the claim. The pod goes first and is
// waited for: a claim deleted from under a running pod stays in Terminating
// until the pod releases it, and the wait keeps the volume's teardown — which
// takes the ReplicatedVolume with it — from starting behind the suite's back.
//
// A pod that outlives the wait is only logged: whatever is left belongs to the
// test namespace, whose deletion cascades over both objects.
func (w *PodIOWorkload) deleteObjects(ctx context.Context) error {
	if err := w.deleteObject(ctx, "Pod", w.podName, &corev1.Pod{}); err != nil {
		return err
	}

	err := w.pollUntil(ctx, w.opts.StopTimeout, "the writer pod to be gone", func() (bool, error) {
		st, err := w.podState(ctx)
		if err != nil {
			return false, err
		}
		return !st.Found, nil
	})
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "[%s] [pod-io-workload] %v; leaving it to the namespace teardown\n",
			time.Now().Format("15:04:05.000"), err)
	}

	return w.deleteObject(ctx, "PersistentVolumeClaim", w.pvcName, &corev1.PersistentVolumeClaim{})
}

// deleteObject deletes one object, tolerating an object that is already gone.
func (w *PodIOWorkload) deleteObject(ctx context.Context, kind, name string, obj client.Object) error {
	obj.SetNamespace(w.opts.Namespace)
	obj.SetName(name)
	if err := w.cl.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting %s %s/%s: %w", kind, w.opts.Namespace, name, err)
	}
	return nil
}
