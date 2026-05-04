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

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/debug"
)

const podNamespace = "d8-sds-replicated-volume"

var (
	rvGVK    = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedVolume")
	rvrGVK   = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedVolumeReplica")
	rvaGVK   = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedVolumeAttachment")
	rscGVK   = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedStorageClass")
	rspGVK   = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedStoragePool")
	drbdrGVK = v1alpha1.SchemeGroupVersion.WithKind("DRBDResource")
	llvGVK   = schema.GroupVersionKind{Group: "storage.deckhouse.io", Version: "v1alpha1", Kind: "LVMLogicalVolume"}
)

var shortNameToGVK = map[string]schema.GroupVersionKind{
	"rv":    rvGVK,
	"rvr":   rvrGVK,
	"rva":   rvaGVK,
	"rsc":   rscGVK,
	"rsp":   rspGVK,
	"drbdr": drbdrGVK,
	"llv":   llvGVK,
}

type target struct {
	kind    string // short name: "rv", "rvr", etc.
	name    string // empty = watch all objects of this kind
	related bool   // "+" suffix: watch children too
}

func main() {
	os.Exit(run())
}

func run() int {
	var (
		logPath      string
		snapshotsDir string
		noLogs       bool
		noColor      bool
	)

	var positional []string
	for _, arg := range os.Args[1:] {
		switch {
		case strings.HasPrefix(arg, "--log="):
			logPath = strings.TrimPrefix(arg, "--log=")
		case strings.HasPrefix(arg, "--snapshots="):
			snapshotsDir = strings.TrimPrefix(arg, "--snapshots=")
		case arg == "--no-logs":
			noLogs = true
		case arg == "--no-color":
			noColor = true
		case arg == "--help" || arg == "-h":
			printUsage()
			return 0
		case strings.HasPrefix(arg, "--"):
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", arg)
			return 1
		default:
			positional = append(positional, arg)
		}
	}

	if len(positional) == 0 {
		printUsage()
		return 1
	}

	if noColor {
		debug.DisableColors()
	}

	targets := parseTargets(positional)

	registerKindMappings()

	c, ctx, cancel, err := setupCache()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer cancel()

	dbg, cleanup, err := setupDebugger(c, snapshotsDir, logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer cleanup()

	if err := watchTargets(dbg, targets); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	if !noLogs {
		cfg := ctrl.GetConfigOrDie()
		clientset, csErr := kubernetes.NewForConfig(cfg)
		if csErr != nil {
			fmt.Fprintf(os.Stderr, "failed to create clientset: %v\n", csErr)
			return 1
		}
		dbg.StartLogStreaming(ctx, clientset, podNamespace,
			debug.Component{Name: "controller", LabelSelector: "app=controller"},
			debug.Component{Name: "agent", LabelSelector: "app=agent"},
		)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Fprintln(os.Stderr, "\nshutting down...")
	dbg.Stop()
	return 0
}

func setupCache() (cache.Cache, context.Context, context.CancelFunc, error) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cfg := ctrl.GetConfigOrDie()

	c, err := cache.New(cfg, cache.Options{Scheme: scheme})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create cache: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if startErr := c.Start(ctx); startErr != nil {
			fmt.Fprintf(os.Stderr, "cache failed: %v\n", startErr)
		}
	}()

	if !c.WaitForCacheSync(ctx) {
		cancel()
		return nil, nil, nil, fmt.Errorf("cache sync failed")
	}

	return c, ctx, cancel, nil
}

func setupDebugger(c cache.Cache, snapshotsDir, logPath string) (*debug.Debugger, func(), error) {
	var opts []debug.Option
	if snapshotsDir != "" {
		if mkErr := os.MkdirAll(snapshotsDir, 0o755); mkErr != nil {
			return nil, nil, fmt.Errorf("failed to create snapshots dir: %w", mkErr)
		}
		opts = append(opts, debug.WithSnapshots(snapshotsDir))
	}
	opts = append(opts, debug.WithRelationGraph(buildRelationGraph()))

	cleanup := func() {}
	if logPath != "" {
		logFile, openErr := os.Create(logPath)
		if openErr != nil {
			return nil, nil, fmt.Errorf("failed to open log file: %w", openErr)
		}
		cleanup = func() { logFile.Close() }
		opts = append(opts, debug.WithEmitter(debug.NewWriterEmitter(os.Stdout, logFile)))
	}

	dbg := debug.New(c, os.Stdout, opts...)
	return dbg, cleanup, nil
}

func watchTargets(dbg *debug.Debugger, targets []target) error {
	for _, t := range targets {
		gvk, ok := shortNameToGVK[t.kind]
		if !ok {
			return fmt.Errorf("unknown kind: %s (known: %s)", t.kind, knownKindsList())
		}

		switch {
		case t.related:
			if t.name == "" {
				if watchErr := dbg.WatchByLabel(gvk, ""); watchErr != nil {
					return fmt.Errorf("watch %s+: %w", t.kind, watchErr)
				}
			} else {
				obj := syntheticObject(gvk, t.name)
				if watchErr := dbg.WatchRelated(obj); watchErr != nil {
					return fmt.Errorf("watch %s/%s+: %w", t.kind, t.name, watchErr)
				}
			}
		case t.name != "":
			obj := syntheticObject(gvk, t.name)
			if watchErr := dbg.Watch(obj); watchErr != nil {
				return fmt.Errorf("watch %s/%s: %w", t.kind, t.name, watchErr)
			}
		default:
			if watchErr := dbg.WatchByLabel(gvk, ""); watchErr != nil {
				return fmt.Errorf("watch %s: %w", t.kind, watchErr)
			}
		}
	}
	return nil
}

func parseTargets(args []string) []target {
	targets := make([]target, 0, len(args))
	for _, arg := range args {
		var t target
		if strings.HasSuffix(arg, "+") {
			t.related = true
			arg = strings.TrimSuffix(arg, "+")
		}
		parts := strings.SplitN(arg, "/", 2)
		t.kind = parts[0]
		if len(parts) == 2 {
			t.name = parts[1]
		}
		targets = append(targets, t)
	}
	return targets
}

func syntheticObject(gvk schema.GroupVersionKind, name string) client.Object {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	return obj
}

func registerKindMappings() {
	debug.RegisterKind("rv", "ReplicatedVolume")
	debug.RegisterKind("rvr", "ReplicatedVolumeReplica")
	debug.RegisterKind("rva", "ReplicatedVolumeAttachment")
	debug.RegisterKind("rsc", "ReplicatedStorageClass")
	debug.RegisterKind("rsp", "ReplicatedStoragePool")
	debug.RegisterKind("drbdr", "DRBDResource")
	debug.RegisterKind("llv", "LVMLogicalVolume")
}

func buildRelationGraph() debug.RelationGraph {
	return debug.RelationGraph{
		rvGVK: {
			{GVK: rvrGVK, Strategy: debug.MatchByLabel, LabelKey: v1alpha1.ReplicatedVolumeLabelKey},
			{GVK: rvaGVK, Strategy: debug.MatchBySpecField, SpecFieldPath: "spec.replicatedVolumeName"},
			{GVK: llvGVK, Strategy: debug.MatchByLabel, LabelKey: v1alpha1.ReplicatedVolumeLabelKey},
			{GVK: drbdrGVK, Strategy: debug.MatchByLabel, LabelKey: v1alpha1.ReplicatedVolumeLabelKey},
		},
		rvrGVK: {
			{GVK: drbdrGVK, Strategy: debug.MatchByLabel, LabelKey: v1alpha1.ReplicatedVolumeLabelKey},
		},
		rscGVK: {},
	}
}

func knownKindsList() string {
	names := make([]string, 0, len(shortNameToGVK))
	for k := range shortNameToGVK {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: %s [flags] <target> [<target> ...]

Targets:
  <kind>            watch all objects of that kind
  <kind>/<name>     watch a specific named object
  <kind>+           watch all objects + related children
  <kind>/<name>+    watch a specific object + related children

Kinds: rv, rvr, rva, rsc, rsp, drbdr, llv

Flags:
  --log=<path>        write plain-text copy to file
  --snapshots=<dir>   save full object snapshots to files
  --no-logs           disable controller/agent log streaming
  --no-color          disable ANSI colors

Examples:
  %[1]s rv rsc
  %[1]s rv/my-volume+
  %[1]s rsc rv/my-volume+ --snapshots=/tmp/snaps
  %[1]s rsp --no-logs
`, os.Args[0])
}
