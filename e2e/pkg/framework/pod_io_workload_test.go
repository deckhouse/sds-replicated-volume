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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testPodIONS       = "e2e-ns"
	testPodIOName     = "e2e-io0"
	testPodIOSC       = "e2e-rsc"
	testPodIOVolume   = "pvc-52ad7f1c"
	testPodIOPVCName  = testPodIOName + podIOPVCSuffix
	testPodIOPodName  = testPodIOName + podIOPodSuffix
	testPodIODigest   = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	testPodIONowMS    = 1750000000000
	testPodIODataSize = 65536
)

// podIOScheme serves the two core kinds the workload creates.
func podIOScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	return scheme
}

// fakeIOPod models the writer pod's view of the volume: the beat journal, the
// data file's recorded and current digests, and the pod's own clock. It answers
// the exec commands the workload issues, so the whole lifecycle runs without a
// cluster.
type fakeIOPod struct {
	nowMS    int64
	journal  []string
	sequence int64

	recorded string
	actual   string
	size     int64

	beatOnProbe bool
	probeExit   int
	probeErr    error
	stopExit    int
	stopErr     error

	stopped bool
}

// newFakeIOPod returns a pod that has completed one verified write and holds a
// data file whose digest matches what was recorded for it.
func newFakeIOPod() *fakeIOPod {
	p := &fakeIOPod{
		nowMS:    testPodIONowMS,
		recorded: testPodIODigest,
		actual:   testPodIODigest,
		size:     testPodIODataSize,
	}
	p.journal = []string{fmt.Sprintf("start %d 1 %s 0:0 %d", p.nowMS, podIODir+"/data", p.size)}
	p.beat()
	return p
}

// beat appends one verified write, one second after the last one.
func (p *fakeIOPod) beat() {
	p.nowMS += 1000
	p.journal = append(p.journal, fmt.Sprintf("ok %d 0 %d a1b2c3d4", p.sequence, p.nowMS))
	p.sequence++
}

// freeze advances the pod's clock without writing anything, which is what a
// frozen volume looks like in the journal: a gap between two beats.
func (p *fakeIOPod) freeze(d time.Duration) {
	p.nowMS += d.Milliseconds()
}

// failIO makes the writer report a broken data path, as its die() does.
func (p *fakeIOPod) failIO(message string) {
	p.nowMS += 1000
	p.journal = append(p.journal, fmt.Sprintf("fail %d io: %s", p.nowMS, message))
}

func (p *fakeIOPod) probeOutput() string {
	var b strings.Builder
	fmt.Fprintf(&b, "#now %d\n#journal\n", p.nowMS)
	for _, line := range p.journal {
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (p *fakeIOPod) checksumOutput() string {
	return fmt.Sprintf("#recorded %s\n#actual %s\n#size %d\n", p.recorded, p.actual, p.size)
}

func (p *fakeIOPod) respond(call execCall) (ExecResult, error) {
	switch {
	case strings.HasPrefix(call.Display, "pod-io-workload probe"):
		if p.beatOnProbe && !p.stopped {
			p.beat()
		}
		return ExecResult{Stdout: p.probeOutput(), ExitCode: p.probeExit}, p.probeErr

	case strings.HasPrefix(call.Display, "pod-io-workload checksum"):
		return ExecResult{Stdout: p.checksumOutput()}, nil

	case strings.HasPrefix(call.Display, "pod-io-workload stop"):
		if p.stopErr == nil && p.stopExit == 0 && !p.stopped {
			p.stopped = true
			p.nowMS += 100
			p.journal = append(p.journal, fmt.Sprintf("stopped %d %d", p.sequence, p.nowMS))
		}
		return ExecResult{Stdout: "#stopping\n", ExitCode: p.stopExit}, p.stopErr
	}

	Fail("unexpected command: " + call.Display)
	return ExecResult{}, nil
}

// boundPVC is the claim as the provisioner leaves it: bound, naming its volume.
func boundPVC() *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: testPodIOPVCName, Namespace: testPodIONS},
		Spec:       corev1.PersistentVolumeClaimSpec{VolumeName: testPodIOVolume},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
}

// runningWriterPod is the pod with its writer container up and ready.
func runningWriterPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: testPodIOPodName, Namespace: testPodIONS},
		Spec:       corev1.PodSpec{NodeName: testNode},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  podIOContainerName,
				Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}

// newTestPodIOWorkload wires a workload to the fake pod and a fake cluster
// holding objs. Polling is fast so negative cases finish quickly.
func newTestPodIOWorkload(pod *fakeIOPod, objs ...client.Object) (*PodIOWorkload, *stubRunner, client.Client) {
	stub := &stubRunner{}
	if pod != nil {
		stub.respond = pod.respond
	}
	cl := fake.NewClientBuilder().WithScheme(podIOScheme()).WithObjects(objs...).Build()

	f := &Framework{nodeRun: stub}
	w, err := f.newPodIOWorkload(cl, PodIOWorkloadOptions{
		Namespace:        testPodIONS,
		StorageClassName: testPodIOSC,
		Name:             testPodIOName,
		RunningTimeout:   100 * time.Millisecond,
		StartTimeout:     100 * time.Millisecond,
		StopTimeout:      100 * time.Millisecond,
	})
	Expect(err).NotTo(HaveOccurred())
	w.poll = time.Millisecond
	return w, stub, cl
}

var _ = Describe("PodIOWorkload options", func() {
	f := &Framework{}

	DescribeTable("refuses options that cannot produce a writer",
		func(mutate func(o *PodIOWorkloadOptions), wantMsg string) {
			opts := PodIOWorkloadOptions{
				Namespace:        testPodIONS,
				StorageClassName: testPodIOSC,
				Name:             testPodIOName,
			}
			mutate(&opts)

			_, err := f.newPodIOWorkload(nil, opts)

			Expect(err).To(MatchError(ContainSubstring(wantMsg)))
		},
		Entry("no namespace", func(o *PodIOWorkloadOptions) { o.Namespace = "" }, "Namespace must not be empty"),
		Entry("no storage class", func(o *PodIOWorkloadOptions) { o.StorageClassName = "" },
			"StorageClassName must not be empty"),
		Entry("name with a slash", func(o *PodIOWorkloadOptions) { o.Name = "e2e/io" }, `Name "e2e/io" must match`),
		Entry("name in upper case", func(o *PodIOWorkloadOptions) { o.Name = "E2E-io" }, `must match`),
		Entry("name too long", func(o *PodIOWorkloadOptions) { o.Name = strings.Repeat("a", podIOMaxNameLen+1) },
			fmt.Sprintf("at most %d fit an object name", podIOMaxNameLen)),
		Entry("size that is not a quantity", func(o *PodIOWorkloadOptions) { o.Size = "1 gigabyte" },
			`Size "1 gigabyte" is not a Kubernetes quantity`),
		Entry("zero size", func(o *PodIOWorkloadOptions) { o.Size = "0" }, "must be positive"),
		Entry("negative interval", func(o *PodIOWorkloadOptions) { o.Interval = -time.Second },
			"Interval must be positive"),
		Entry("negative freeze tolerance", func(o *PodIOWorkloadOptions) { o.MaxHeartbeatGap = -time.Second },
			"MaxHeartbeatGap must be positive"),
		Entry("negative data size", func(o *PodIOWorkloadOptions) { o.DataKiB = -1 }, "DataKiB must be at least 1"),
	)

	It("defaults the size, the image, the beat rate and the freeze tolerance", func() {
		w, err := f.newPodIOWorkload(nil, PodIOWorkloadOptions{
			Namespace:        testPodIONS,
			StorageClassName: testPodIOSC,
			Name:             testPodIOName,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(w.opts.Size).To(Equal(DefaultVolumeSize))
		Expect(w.image).To(Equal(defaultIOImage))
		Expect(w.opts.Interval).To(Equal(podIODefaultInterval))
		Expect(w.opts.MaxHeartbeatGap).To(Equal(ioWorkloadDefaultMaxGap),
			"a freeze must mean the same thing as for the node-level writer")
		Expect(w.pvcName).To(Equal(testPodIOPVCName))
		Expect(w.podName).To(Equal(testPodIOPodName))
	})

	It("takes the image from E2E_UPGRADE_IMAGE when the stand cannot reach Docker Hub", func() {
		GinkgoT().Setenv(EnvUpgradeImage, "registry.internal/busybox:1.36")

		w, err := f.newPodIOWorkload(nil, PodIOWorkloadOptions{
			Namespace:        testPodIONS,
			StorageClassName: testPodIOSC,
			Name:             testPodIOName,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(w.image).To(Equal("registry.internal/busybox:1.36"))
	})

	It("lets an explicit image win over the environment", func() {
		GinkgoT().Setenv(EnvUpgradeImage, "registry.internal/busybox:1.36")

		w, err := f.newPodIOWorkload(nil, PodIOWorkloadOptions{
			Namespace:        testPodIONS,
			StorageClassName: testPodIOSC,
			Name:             testPodIOName,
			Image:            "registry.other/busybox:1.37",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(w.image).To(Equal("registry.other/busybox:1.37"))
	})
})

var _ = Describe("PodIOWorkload manifests", func() {
	It("asks for a filesystem volume of the requested class and size", func() {
		w, err := (&Framework{}).newPodIOWorkload(nil, PodIOWorkloadOptions{
			Namespace:        testPodIONS,
			StorageClassName: testPodIOSC,
			Name:             testPodIOName,
			Size:             "3Gi",
		})
		Expect(err).NotTo(HaveOccurred())

		pvc := w.buildPVC()

		Expect(pvc.Name).To(Equal(testPodIOPVCName))
		Expect(pvc.Namespace).To(Equal(testPodIONS))
		Expect(pvc.Spec.StorageClassName).To(HaveValue(Equal(testPodIOSC)))
		Expect(pvc.Spec.VolumeMode).To(HaveValue(Equal(corev1.PersistentVolumeFilesystem)))
		Expect(pvc.Spec.AccessModes).To(Equal([]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}))
		size := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		Expect(size.String()).To(Equal("3Gi"))
		Expect(pvc.Labels).To(HaveKey(LabelE2ERunKey), "a leftover claim has to be recognizable")
	})

	It("runs the writer on the volume with a pull policy that survives a registry outage", func() {
		w, _, _ := newTestPodIOWorkload(nil)

		pod := w.buildPod()

		Expect(pod.Name).To(Equal(testPodIOPodName))
		Expect(pod.Namespace).To(Equal(testPodIONS))
		Expect(pod.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyAlways))
		Expect(pod.Spec.Containers).To(HaveLen(1))
		container := pod.Spec.Containers[0]
		Expect(container.Name).To(Equal(podIOContainerName))
		Expect(container.Image).To(Equal(defaultIOImage))
		Expect(container.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent),
			"the default image is tagged latest, whose implied Always ties every pod to the registry")
		Expect(container.Command[:2]).To(Equal([]string{"sh", "-c"}))
		Expect(container.Command[2]).To(Equal(w.program()))
		Expect(container.VolumeMounts).To(ConsistOf(corev1.VolumeMount{
			Name:      podIOVolumeName,
			MountPath: podIOMountPath,
		}))
		Expect(pod.Spec.Volumes).To(HaveLen(1))
		Expect(pod.Spec.Volumes[0].PersistentVolumeClaim).To(HaveValue(
			Equal(corev1.PersistentVolumeClaimVolumeSource{ClaimName: testPodIOPVCName})))
		Expect(pod.Labels).To(HaveKey(LabelE2ERunKey))
	})

	It("puts the image from the environment into the pod", func() {
		GinkgoT().Setenv(EnvUpgradeImage, "registry.internal/busybox:1.36")
		w, _, _ := newTestPodIOWorkload(nil)

		Expect(w.buildPod().Spec.Containers[0].Image).To(Equal("registry.internal/busybox:1.36"))
	})
})

var _ = Describe("PodIOWorkload start", func() {
	It("adopts the claim and the pod it already has, and records the volume behind them",
		func(ctx SpecContext) {
			pod := newFakeIOPod()
			w, stub, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

			Expect(w.start(ctx)).To(Succeed())

			Expect(w.VolumeName()).To(Equal(testPodIOVolume),
				"the ReplicatedVolume is addressed through the claim's spec.volumeName")
			Expect(stub.countKind(execKindPod)).To(BeNumerically(">", 0))
			Expect(stub.countKind(execKindPodNoRetry)).To(BeZero(), "starting changes nothing inside the pod")
			for _, call := range stub.calls {
				Expect(call.Namespace).To(Equal(testPodIONS))
				Expect(call.Pod).To(Equal(testPodIOPodName))
				Expect(call.Container).To(Equal(podIOContainerName))
			}
		})

	It("creates both objects when the namespace is empty", func(ctx SpecContext) {
		pod := newFakeIOPod()
		w, _, cl := newTestPodIOWorkload(pod)

		// Both objects are created, and then nothing in a fake cluster schedules
		// the pod or binds the claim: the wait for a running writer is what fails,
		// after both objects exist.
		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("the writer pod to be running")))
		Expect(readPodIOObject(ctx, cl, testPodIOPVCName, &corev1.PersistentVolumeClaim{})).To(Succeed())
		Expect(readPodIOObject(ctx, cl, testPodIOPodName, &corev1.Pod{})).To(Succeed())
	})

	It("refuses to work with a claim that names no volume", func(ctx SpecContext) {
		// Without spec.volumeName there is no ReplicatedVolume to address, so the
		// suite could not tell which volume this workload's evidence is about.
		unbound := boundPVC()
		unbound.Spec.VolumeName = ""
		unbound.Status.Phase = corev1.ClaimPending
		w, _, _ := newTestPodIOWorkload(newFakeIOPod(), unbound, runningWriterPod())

		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("names no volume yet")))
		Expect(err).To(MatchError(ContainSubstring(string(corev1.ClaimPending))))
		Expect(w.VolumeName()).To(BeEmpty())
	})

	It("names the image, the container state and the override when the pod does not run",
		func(ctx SpecContext) {
			pending := runningWriterPod()
			pending.Status.Phase = corev1.PodPending
			pending.Status.ContainerStatuses[0].Ready = false
			pending.Status.ContainerStatuses[0].State = corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{
					Reason:  "ImagePullBackOff",
					Message: `Back-off pulling image "busybox:latest"`,
				},
			}
			w, _, _ := newTestPodIOWorkload(newFakeIOPod(), boundPVC(), pending)

			err := w.start(ctx)

			Expect(err).To(MatchError(ContainSubstring("ImagePullBackOff")))
			Expect(err).To(MatchError(ContainSubstring(defaultIOImage)))
			Expect(err).To(MatchError(ContainSubstring(`Back-off pulling image "busybox:latest"`)))
			Expect(err).To(MatchError(ContainSubstring(EnvUpgradeImage)),
				"a stand without Docker Hub is fixed by the override, and the message has to say so")
			Expect(err).To(MatchError(ContainSubstring(string(corev1.PodPending))))
		})

	It("refuses to call a writer started when it never wrote", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.journal = pod.journal[:1] // start record only, no verified write
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("the first verified write")))
	})

	It("reports the writer's own failure instead of waiting the budget out", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.journal = pod.journal[:1]
		pod.failIO("input/output error")
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("failed before its first verified write")))
		Expect(err).To(MatchError(ContainSubstring("input/output error")))
	})
})

var _ = Describe("PodIOWorkload volume identity", func() {
	// csiPV is the PersistentVolume as the provisioner leaves it: named after the
	// CSI volume it carries.
	csiPV := func(handle string) *corev1.PersistentVolume {
		return &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: testPodIOVolume},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{VolumeHandle: handle},
				},
			},
		}
	}

	It("accepts a volume the driver named after the claim's spec.volumeName", func(ctx SpecContext) {
		w, _, _ := newTestPodIOWorkload(nil, csiPV(testPodIOVolume))
		w.volumeName = testPodIOVolume

		Expect(w.verifyVolumeHandle(ctx)).To(Succeed())
	})

	It("reports a driver that started naming volumes differently", func(ctx SpecContext) {
		w, _, _ := newTestPodIOWorkload(nil, csiPV("some-other-id"))
		w.volumeName = testPodIOVolume

		err := w.verifyVolumeHandle(ctx)

		Expect(err).To(MatchError(ContainSubstring("some-other-id")))
		Expect(err).To(MatchError(ContainSubstring("not the ReplicatedVolume's name")))
	})

	It("reports a volume that is not a CSI volume at all", func(ctx SpecContext) {
		pv := csiPV(testPodIOVolume)
		pv.Spec.CSI = nil
		w, _, _ := newTestPodIOWorkload(nil, pv)
		w.volumeName = testPodIOVolume

		Expect(w.verifyVolumeHandle(ctx)).To(MatchError(ContainSubstring("is not a CSI volume")))
	})

	It("has nothing to verify before the claim was bound", func(ctx SpecContext) {
		w, _, _ := newTestPodIOWorkload(nil)

		Expect(w.verifyVolumeHandle(ctx)).To(MatchError(ContainSubstring("names no volume yet")))
	})
})

var _ = Describe("PodIOWorkload progress", func() {
	It("waits for more verified writes and reports the status that satisfied it", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.beatOnProbe = true
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		st, err := w.awaitProgress(ctx, 3)

		Expect(err).NotTo(HaveOccurred())
		Expect(st.LastSequence).To(BeNumerically(">=", 3))
		Expect(st.GapExceeded).To(BeFalse())
	})

	It("fails on a freeze that already ended, not only on one that is going on", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.freeze(2 * time.Minute)
		pod.beat() // writes resumed: the freeze is history, and must still be a verdict
		pod.beatOnProbe = true
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		_, err := w.awaitProgress(ctx, 1)

		// Two minutes of freeze plus the second the resuming beat took.
		Expect(err).To(MatchError(ContainSubstring("stalled for 2m1s")))
		Expect(err).To(MatchError(ContainSubstring("tolerated max 30s")))
	})

	It("fails when the writer breaks while we wait", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.failIO("input/output error")
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		_, err := w.awaitProgress(ctx, 1)

		Expect(err).To(MatchError(ContainSubstring("the writer failed")))
		Expect(err).To(MatchError(ContainSubstring("input/output error")))
	})

	It("reports a probe that could not run at all", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.probeExit = 126
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		_, err := w.observe(ctx)

		Expect(err).To(MatchError(ContainSubstring("exited with code 126")))
	})
})

var _ = Describe("PodIOWorkload freeze analysis", func() {
	// Every entry lists the pause BEFORE each beat; beat() then advances the pod's
	// clock by the one-second beat interval, so the distance between two beats is
	// the pause in front of the later one plus that second.
	DescribeTable("measures the gaps of the WHOLE journal",
		func(ctx SpecContext, beats []time.Duration, wantMax time.Duration, wantFreezes int) {
			pod := newFakeIOPod()
			pod.journal = pod.journal[:1]
			pod.sequence = 0
			for _, gap := range beats {
				pod.freeze(gap)
				pod.beat()
			}
			w, stub, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

			maxGap, freezes, err := w.analyzeFreezes(ctx)

			Expect(err).NotTo(HaveOccurred())
			Expect(maxGap).To(Equal(wantMax))
			Expect(freezes).To(HaveLen(wantFreezes))
			Expect(stub.calls).To(HaveLen(1))
			Expect(stub.calls[0].Cmd[2]).To(ContainSubstring(fmt.Sprintf("tail -n %d", ioWorkloadJournalFull)),
				"a tail would lose a freeze whose boundary scrolled out of it")
		},
		Entry("a steady writer", []time.Duration{0, 0, 0}, time.Second, 0),
		Entry("a gap in the middle", []time.Duration{0, time.Minute, 0}, 61*time.Second, 1),
		Entry("a gap that ended before the last beat", []time.Duration{0, 45 * time.Second, 0, 0},
			46*time.Second, 1),
		Entry("two gaps", []time.Duration{0, time.Minute, 2 * time.Minute, 0}, 121*time.Second, 2),
		Entry("a gap exactly at the tolerance is not a freeze",
			[]time.Duration{0, 29 * time.Second, 0}, 30*time.Second, 0),
	)

	It("names the beats a freeze happened between", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.beat()
		pod.freeze(90 * time.Second)
		pod.beat()
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		maxGap, freezes, err := w.analyzeFreezes(ctx)

		Expect(err).NotTo(HaveOccurred())
		Expect(maxGap).To(Equal(91 * time.Second))
		Expect(freezes).To(HaveLen(1))
		Expect(freezes[0].FromSequence).To(Equal(int64(1)))
		Expect(freezes[0].ToSequence).To(Equal(int64(2)))
		Expect(freezes[0].String()).To(ContainSubstring("between beats 1 and 2"))
	})
})

var _ = Describe("PodIOWorkload checksum", func() {
	It("accepts a data file that still hashes to what was recorded", func(ctx SpecContext) {
		w, stub, _ := newTestPodIOWorkload(newFakeIOPod(), boundPVC(), runningWriterPod())

		Expect(w.verifyChecksum(ctx)).To(Succeed())

		Expect(stub.countKind(execKindPod)).To(Equal(1), "re-reading the data is a read, so it may retry")
		Expect(stub.calls[0].Cmd[2]).To(ContainSubstring("sha256sum <" + w.dataPath()))
	})

	It("reports a corruption with both digests, the path and the volume", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.actual = "deadbeef" + strings.Repeat("0", 56)
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())
		w.volumeName = testPodIOVolume

		err := w.verifyChecksum(ctx)

		Expect(err).To(MatchError(ContainSubstring("corrupted")))
		Expect(err).To(MatchError(ContainSubstring(pod.actual)))
		Expect(err).To(MatchError(ContainSubstring(testPodIODigest)))
		Expect(err).To(MatchError(ContainSubstring(w.dataPath())))
		Expect(err).To(MatchError(ContainSubstring(testPodIOVolume)))
	})

	It("does not pass a volume whose data file vanished", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.actual = ""
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		err := w.verifyChecksum(ctx)

		Expect(err).To(MatchError(ContainSubstring("could not be read back")))
	})

	It("does not pass a writer that never recorded a digest", func(ctx SpecContext) {
		pod := newFakeIOPod()
		pod.recorded = ""
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		err := w.verifyChecksum(ctx)

		Expect(err).To(MatchError(ContainSubstring("recorded no checksum")))
	})

	DescribeTable("parses the checksum envelope",
		func(out string, want podIOChecksum, wantErr string) {
			got, err := parsePodIOChecksum(out)

			if wantErr != "" {
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("complete", "#recorded aa\n#actual bb\n#size 64\n",
			podIOChecksum{Recorded: "aa", Actual: "bb", Size: 64}, ""),
		Entry("missing files leave empty values", "#recorded \n#actual \n#size \n",
			podIOChecksum{Size: -1}, ""),
		Entry("carriage returns are tolerated", "#recorded aa\r\n#actual aa\r\n#size 64\r\n",
			podIOChecksum{Recorded: "aa", Actual: "aa", Size: 64}, ""),
		Entry("no envelope at all", "sh: not found\n", podIOChecksum{}, "carries no #recorded line"),
		Entry("unparsable size", "#recorded aa\n#actual aa\n#size huge\n", podIOChecksum{},
			"unparsable data size"),
	)
})

var _ = Describe("PodIOWorkload stop and cleanup", func() {
	It("asks the writer to stop exactly once, through the no-retry seam", func(ctx SpecContext) {
		pod := newFakeIOPod()
		w, stub, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())

		Expect(w.stop(ctx)).To(Succeed())

		Expect(stub.countKind(execKindPodNoRetry)).To(Equal(1),
			"the only state-changing command must never be executed twice")
		Expect(stub.countDisplaysWithPrefix("pod-io-workload stop")).To(Equal(1))
		Expect(pod.stopped).To(BeTrue())
	})

	It("is idempotent once the writer had its last word", func(ctx SpecContext) {
		pod := newFakeIOPod()
		w, stub, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())
		Expect(w.stop(ctx)).To(Succeed())
		before := stub.countKind(execKindPodNoRetry)

		Expect(w.stop(ctx)).To(Succeed())

		Expect(stub.countKind(execKindPodNoRetry)).To(Equal(before))
	})

	It("stops the writer, judges the whole journal and deletes the pod before the claim",
		func(ctx SpecContext) {
			pod := newFakeIOPod()
			w, stub, cl := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())
			Expect(w.start(ctx)).To(Succeed())

			Expect(w.cleanup(ctx)).To(Succeed())

			Expect(pod.stopped).To(BeTrue())
			Expect(apierrors.IsNotFound(readPodIOObject(ctx, cl, testPodIOPodName, &corev1.Pod{}))).To(BeTrue())
			Expect(apierrors.IsNotFound(
				readPodIOObject(ctx, cl, testPodIOPVCName, &corev1.PersistentVolumeClaim{}))).To(BeTrue())
			Expect(stub.indexOfDisplayPrefix("pod-io-workload stop")).
				To(BeNumerically("<", lastIndexOfDisplayPrefix(stub, "pod-io-workload probe")),
					"the journal is read after the writer was told to stop, while the pod is still there")
		})

	It("does the whole teardown only once", func(ctx SpecContext) {
		pod := newFakeIOPod()
		w, stub, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())
		Expect(w.start(ctx)).To(Succeed())
		Expect(w.cleanup(ctx)).To(Succeed())
		before := len(stub.calls)

		Expect(w.cleanup(ctx)).To(Succeed())

		Expect(stub.calls).To(HaveLen(before))
	})

	It("fails the run when the writer stalled at some point of it", func(ctx SpecContext) {
		pod := newFakeIOPod()
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())
		Expect(w.start(ctx)).To(Succeed())
		pod.freeze(3 * time.Minute)
		pod.beat()

		err := w.cleanup(ctx)

		Expect(err).To(MatchError(ContainSubstring("stalled for 3m1s during the run")))
		Expect(err).To(MatchError(ContainSubstring("between beats")))
	})

	It("fails the run when the writer's data path broke", func(ctx SpecContext) {
		pod := newFakeIOPod()
		w, _, _ := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())
		Expect(w.start(ctx)).To(Succeed())
		pod.failIO("input/output error")

		err := w.cleanup(ctx)

		Expect(err).To(MatchError(ContainSubstring("the writer failed")))
		Expect(err).To(MatchError(ContainSubstring(w.journalPath())))
	})

	It("does not turn a writer that died mid-run into a green result", func(ctx SpecContext) {
		pod := newFakeIOPod()
		w, _, cl := newTestPodIOWorkload(pod, boundPVC(), runningWriterPod())
		Expect(w.start(ctx)).To(Succeed())
		var crashed corev1.Pod
		Expect(readPodIOObject(ctx, cl, testPodIOPodName, &crashed)).To(Succeed())
		crashed.Status.ContainerStatuses[0].Ready = false
		crashed.Status.ContainerStatuses[0].State = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"},
		}
		Expect(cl.Status().Update(ctx, &crashed)).To(Succeed())

		err := w.cleanup(ctx)

		Expect(err).To(MatchError(ContainSubstring("journal could not be read at cleanup")))
		Expect(err).To(MatchError(ContainSubstring("OOMKilled")))
	})

	It("says nothing about a workload whose start never got anywhere", func(ctx SpecContext) {
		w, stub, _ := newTestPodIOWorkload(nil)

		Expect(w.cleanup(ctx)).To(Succeed())

		Expect(stub.calls).To(BeEmpty(), "there is no journal to judge, and no pod to exec into")
	})
})

var _ = Describe("projectPodIOPod", func() {
	It("quotes the reason an image cannot be pulled", func() {
		pod := runningWriterPod()
		pod.Status.Phase = corev1.PodPending
		pod.Status.ContainerStatuses[0].Ready = false
		pod.Status.ContainerStatuses[0].State = corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull", Message: "no such host"},
		}

		st := projectPodIOPod(pod)

		Expect(st.Ready).To(BeFalse())
		Expect(st.Phase).To(Equal(corev1.PodPending))
		Expect(st.Note).To(Equal("waiting: ErrImagePull: no such host"))
		Expect(st.String()).To(ContainSubstring("ErrImagePull"))
	})

	It("keeps the previous termination of a restarted writer", func() {
		pod := runningWriterPod()
		pod.Status.ContainerStatuses[0].RestartCount = 2
		pod.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error", Message: "cannot write"},
		}

		st := projectPodIOPod(pod)

		Expect(st.Ready).To(BeTrue(), "the writer resumes its sequence after a restart")
		Expect(st.Restarts).To(Equal(int32(2)))
		Expect(st.Note).To(ContainSubstring("previously terminated with exit code 1"))
		Expect(st.Note).To(ContainSubstring("cannot write"))
	})

	It("does not call a container ready before it published a status", func() {
		pod := runningWriterPod()
		pod.Status.ContainerStatuses = nil

		st := projectPodIOPod(pod)

		Expect(st.Ready).To(BeFalse())
		Expect(st.Note).To(ContainSubstring("published no status yet"))
	})

	It("reports an absent pod as absent", func() {
		Expect(PodIOPodState{}.String()).To(Equal("absent"))
	})
})

// readPodIOObject reads one of the workload's objects back out of the fake
// cluster.
func readPodIOObject(ctx context.Context, cl client.Client, name string, into client.Object) error {
	return cl.Get(ctx, client.ObjectKey{Namespace: testPodIONS, Name: name}, into)
}

// lastIndexOfDisplayPrefix returns the position of the LAST call whose display
// starts with p, or -1.
func lastIndexOfDisplayPrefix(s *stubRunner, p string) int {
	found := -1
	for i := range s.calls {
		if strings.HasPrefix(s.calls[i].Display, p) {
			found = i
		}
	}
	return found
}
