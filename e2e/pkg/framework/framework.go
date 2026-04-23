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

// Package framework provides the shared e2e test framework for datamesh
// operations on a real Kubernetes cluster. It includes fluent builders,
// wait helpers, invariant watchers, and shared behaviors.
package framework

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	dbg "github.com/deckhouse/sds-replicated-volume/e2e/pkg/debug"
)

// ControlPlane distinguishes old (pre-datamesh) and new control plane versions.
type ControlPlane int

const (
	// ControlPlaneOld represents the legacy control plane (pre-datamesh).
	ControlPlaneOld ControlPlane = iota
	// ControlPlaneNew represents the new datamesh-based control plane.
	ControlPlaneNew
)

// Framework holds shared state for e2e tests: Kubernetes client, cluster
// topology, and per-test requirements.
//
// A single Framework instance is created per test suite via Setup() and
// shared across all specs in the package.
type Framework struct {
	Client   client.Client
	Cache    cache.Cache
	Scheme   *runtime.Scheme
	WorkerID int
	Debugger *dbg.Debugger

	Discovery *Discovery

	restConfig   *rest.Config
	clientset    kubernetes.Interface
	cacheCancel  context.CancelFunc
	runID        string // 6-hex-char unique ID shared across parallel workers
	prefix       string // "e2e-{runID}"
	controlPlane ControlPlane

	nsCache      map[string]*TestNS
	rscCache     map[rscCacheKey]*TestRSC
	podCacheMu   sync.Mutex
	podNameCache map[podCacheKey]string
	specCounters map[any]int
}

// Setup creates an empty Framework and registers Ginkgo lifecycle hooks.
//
// Call at package init time (var f = fw.Setup()) — not inside BeforeSuite.
// The returned pointer is populated during SynchronizedBeforeSuite when the
// cluster is discovered. The run ID is generated on worker 1 and broadcast
// to all parallel workers.
func Setup() *Framework {
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	f := &Framework{}

	mult := parseTimeoutMultiplier(os.Getenv("E2E_TIMEOUT_MULTIPLIER"))
	registerTimeoutPolicy(mult)
	registerRequirementsTransformer()
	registerDisruptiveTransformer()

	SynchronizedBeforeSuite(func(_ SpecContext) []byte {
		return []byte(generateRunID())
	}, func(ctx SpecContext, data []byte) {
		f.runID = string(data)
		fmt.Fprintf(GinkgoWriter, "[Setup] BeforeSuite starting f.init() runID=%s...\n", f.runID)
		f.init(ctx)
		fmt.Fprintln(GinkgoWriter, "[Setup] BeforeSuite f.init() done")
	})

	SynchronizedAfterSuite(func(ctx SpecContext) {
		f.cleanupWorkerObjects(ctx)
		if f.Debugger != nil {
			f.Debugger.Stop()
		}
		if GinkgoParallelProcess() != 1 {
			if f.cacheCancel != nil {
				f.cacheCancel()
			}
		}
	}, func(ctx SpecContext) {
		f.scavengeAllE2E(ctx)
		if f.cacheCancel != nil {
			f.cacheCancel()
		}
	}, NodeTimeout(60*time.Second))

	BeforeEach(func() {
		f.specCounters = make(map[any]int)
	})

	JustBeforeEach(func() {
		enforceRequirements(f)
		enforceDisruptive()
	})

	return f
}

// generateRunID returns a 6-hex-char random string.
func generateRunID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RunID returns the run ID for this test run.
func (f *Framework) RunID() string {
	return f.runID
}

// Name returns a unique resource name: "e2e-{runID}-{suffix}".
func (f *Framework) Name(suffix string) string {
	return f.prefix + "-" + suffix
}

// init discovers the cluster and populates the Framework fields.
// Called once from BeforeSuite with the suite's SpecContext.
func (f *Framework) init(ctx context.Context) {
	f.WorkerID = GinkgoParallelProcess()
	f.prefix = fmt.Sprintf("e2e-%s", f.runID)
	f.nsCache = make(map[string]*TestNS)
	f.rscCache = make(map[rscCacheKey]*TestRSC)
	f.podNameCache = make(map[podCacheKey]string)

	cacheCtx, cacheCancel := context.WithCancel(context.Background())
	f.cacheCancel = cacheCancel

	cfg := ctrl.GetConfigOrDie()
	cfg.WrapTransport = newRetryTransport(3)
	f.restConfig = cfg

	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(snc.AddToScheme(scheme)).To(Succeed())
	f.Scheme = scheme

	c, err := cache.New(cfg, cache.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	go func() { _ = c.Start(cacheCtx) }()
	Expect(c.WaitForCacheSync(ctx)).To(BeTrue())
	f.Cache = c

	cl, err := client.New(cfg, client.Options{
		Scheme: scheme,
		Cache:  &client.CacheOptions{Reader: c},
	})
	Expect(err).NotTo(HaveOccurred())
	f.Client = cl

	clientset, err := kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
	f.clientset = clientset

	var deploys appsv1.DeploymentList
	Expect(c.List(ctx, &deploys, client.InNamespace("d8-sds-replicated-volume"))).To(Succeed())
	for i := range deploys.Items {
		if deploys.Items[i].Name == "controller" || strings.Contains(deploys.Items[i].Name, "sds-replicated-volume") {
			f.controlPlane = ControlPlaneNew
			break
		}
	}

	relationGraph := dbg.RelationGraph{
		gvkRV: {
			{GVK: gvkRVR, Strategy: dbg.MatchByLabel, LabelKey: v1alpha1.ReplicatedVolumeLabelKey},
			{GVK: gvkRVA, Strategy: dbg.MatchBySpecField, SpecFieldPath: "spec.replicatedVolumeName"},
			{GVK: gvkDRBDR, Strategy: dbg.MatchByLabel, LabelKey: v1alpha1.ReplicatedVolumeLabelKey},
		},
		gvkRVS: {
			{GVK: gvkRVRS, Strategy: dbg.MatchBySpecField, SpecFieldPath: "spec.replicatedVolumeSnapshotName"},
		},
	}

	f.Debugger = dbg.New(c, GinkgoWriter,
		dbg.WithRelationGraph(relationGraph),
	)
	f.Debugger.RegisterKind("rv", gvkRV.Kind)
	f.Debugger.RegisterKind("rvr", gvkRVR.Kind)
	f.Debugger.RegisterKind("rva", gvkRVA.Kind)
	f.Debugger.RegisterKind("rvs", gvkRVS.Kind)
	f.Debugger.RegisterKind("rvrs", gvkRVRS.Kind)
	f.Debugger.RegisterKind("drbdr", gvkDRBDR.Kind)
	f.Debugger.RegisterKind("drbdop", gvkDRBDOp.Kind)
	f.Debugger.RegisterKind("rsc", gvkRSC.Kind)
	f.Debugger.RegisterKind("rsp", gvkRSP.Kind)
	f.Debugger.RegisterKind("llv", gvkLLV.Kind)
	f.Debugger.RegisterKind("ns", gvkNS.Kind)
	f.Debugger.RegisterKind("sc", gvkSC.Kind)
	f.Debugger.StartLogStreaming(context.Background(), clientset, "d8-sds-replicated-volume",
		dbg.Component{Name: "controller", LabelSelector: "app=controller"},
		dbg.Component{Name: "agent", LabelSelector: "app=agent"},
	)

	f.Discovery = newDiscovery(ctx, f)

	fmt.Fprintf(GinkgoWriter, "[%s] init: runID=%s worker=%d rspThick=%s rspThin=%s controlPlane=%d\n",
		time.Now().Format("15:04:05.000"), f.runID, f.WorkerID, f.Discovery.ThickRSPName(), f.Discovery.ThinRSPName(), f.controlPlane)
}

// autoName generates a unique object name for the current spec.
// If name is provided, it replaces the per-type counter suffix.
// The per-type counter uses the typed nil pointer as map key —
// different Go types produce different keys without reflection.
func (f *Framework) autoName(sample client.Object, name ...string) string {
	report := CurrentSpecReport()
	h := fnv.New32a()
	_, _ = h.Write([]byte(report.FullText()))
	base := fmt.Sprintf("%s-%08x-a%d", f.prefix, h.Sum32(), report.NumAttempts)
	if len(name) > 0 && name[0] != "" {
		return base + "-" + name[0]
	}
	f.specCounters[sample]++
	return fmt.Sprintf("%s-%d", base, f.specCounters[sample])
}

// uniqueNameKey is a sentinel type used by UniqueName as the counter key
// in specCounters, separate from any client.Object type.
type uniqueNameKey struct{}

// UniqueName returns a per-spec unique name suitable for arbitrary resources
// (not tied to a Kubernetes object type). Each call within the same spec
// increments an independent counter, producing names like
// "e2e-{runID}-{specHash}-a{attempt}-{suffix}" or
// "e2e-{runID}-{specHash}-a{attempt}-{N}" when no suffix is given.
func (f *Framework) UniqueName(suffix ...string) string {
	report := CurrentSpecReport()
	h := fnv.New32a()
	_, _ = h.Write([]byte(report.FullText()))
	base := fmt.Sprintf("%s-%08x-a%d", f.prefix, h.Sum32(), report.NumAttempts)
	if len(suffix) > 0 && suffix[0] != "" {
		return base + "-" + suffix[0]
	}
	f.specCounters[uniqueNameKey{}]++
	return fmt.Sprintf("%s-%d", base, f.specCounters[uniqueNameKey{}])
}

// sanitizeNodeName converts a node name to a DNS-safe lowercase suffix.
func sanitizeNodeName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, name)
}
