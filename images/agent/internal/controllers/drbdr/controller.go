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

package drbdr

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/controlleroptions"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

// BuildController creates and registers the DRBD controller and scanner with the manager.
func BuildController(mgr manager.Manager) error {
	// Register field indexes
	if err := indexes.RegisterDRBDRByNodeName(mgr); err != nil {
		return fmt.Errorf("registering DRBDR index: %w", err)
	}
	if err := indexes.RegisterLVGByNodeName(mgr); err != nil {
		return fmt.Errorf("registering LVG index: %w", err)
	}
	if err := indexes.RegisterLLVByLVGName(mgr); err != nil {
		return fmt.Errorf("registering LLV index: %w", err)
	}

	cfg, err := env.GetConfig()
	if err != nil {
		return fmt.Errorf("getting config: %w", err)
	}

	cl := mgr.GetClient()
	nodeName := cfg.NodeName()

	// Set up drbd command logging at actual execution time.
	// drbdmeta calls are additionally wrapped with strace to diagnose O_EXCL delays.
	origExec := drbdutils.ExecCommandContext
	drbdutils.ExecCommandContext = func(ctx context.Context, name string, arg ...string) drbdutils.Cmd {
		logger := log.FromContext(ctx)
		if name == drbdutils.DRBDMetaCommand {
			return newStraceCmd(ctx, logger, name, arg...)
		}
		return &loggingCmd{
			Cmd: origExec(ctx, name, arg...),
			log: logger,
			cmd: name, args: arg,
		}
	}

	// Create internal request channel (scanner sends here)
	requestCh := make(chan event.TypedGenericEvent[DRBDReconcileRequest], 100)

	// Create scanner with new channel type
	scanner := NewScanner(requestCh)
	if err := mgr.Add(scanner); err != nil {
		return fmt.Errorf("adding scanner runnable: %w", err)
	}

	// Create port cache (reconciler-owned)
	portCache := NewPortCache(context.Background(), PortRangeMin, PortRangeMax)

	// Create reconciler (implements reconcile.TypedReconciler[DRBDReconcileRequest])
	rec := NewReconciler(cl, nodeName, portCache)

	// Build DRBD resource controller with TypedReconciler
	if err := builder.TypedControllerManagedBy[DRBDReconcileRequest](mgr).
		Named(ControllerName).
		WithLogConstructor(func(req *DRBDReconcileRequest) logr.Logger {
			l := mgr.GetLogger().WithValues(
				"controller", ControllerName,
				"controllerGroup", v1alpha1.APIGroup,
				"controllerKind", "DRBDResource",
			)
			if req != nil {
				name := req.Name
				if name == "" {
					name = req.ActualNameOnTheNode
				}
				l = l.WithValues("name", name)
			}
			return l
		}).
		Watches(
			&v1alpha1.DRBDResource{},
			handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []DRBDReconcileRequest {
				dr := obj.(*v1alpha1.DRBDResource)
				return []DRBDReconcileRequest{{Name: dr.Name}}
			}),
			builder.WithPredicates(drbdrPredicates(nodeName)...),
		).
		// Watch internal channel (scanner events) - maps *DRBDReconcileRequest to DRBDReconcileRequest
		WatchesRawSource(
			source.TypedChannel(requestCh, handler.TypedEnqueueRequestsFromMapFunc(
				func(_ context.Context, req DRBDReconcileRequest) []DRBDReconcileRequest {
					return []DRBDReconcileRequest{req}
				},
			)),
		).
		WithOptions(controller.TypedOptions[DRBDReconcileRequest]{
			MaxConcurrentReconciles: 10,
			RateLimiter:             controlleroptions.DefaultRateLimiter[DRBDReconcileRequest](),
		}).
		Complete(withDurationLogging(rec)); err != nil {
		return fmt.Errorf("building DRBD resource controller: %w", err)
	}

	return nil
}

func withDurationLogging(inner reconcile.TypedReconciler[DRBDReconcileRequest]) reconcile.TypedReconciler[DRBDReconcileRequest] {
	return reconcile.TypedFunc[DRBDReconcileRequest](func(ctx context.Context, req DRBDReconcileRequest) (reconcile.Result, error) {
		start := time.Now()
		res, err := inner.Reconcile(ctx, req)
		log.FromContext(ctx).Info("Reconcile complete", "duration", time.Since(start).String())
		return res, err
	})
}

type loggingCmd struct {
	drbdutils.Cmd
	log        logr.Logger
	cmd        string
	args       []string
	loggedOnce bool
	startTime  time.Time
}

func (c *loggingCmd) logExec() {
	if !c.loggedOnce {
		c.loggedOnce = true
		c.log.Info("Executing DRBD command", "command", c.cmd, "args", c.args)
	}
}

func (c *loggingCmd) CombinedOutput() ([]byte, error) {
	c.logExec()
	start := time.Now()
	out, err := c.Cmd.CombinedOutput()
	c.logDone(start, err)
	return out, err
}

func (c *loggingCmd) Start() error {
	c.logExec()
	c.startTime = time.Now()
	return c.Cmd.Start()
}

func (c *loggingCmd) StdoutPipe() (io.ReadCloser, error) {
	return c.Cmd.StdoutPipe()
}

func (c *loggingCmd) Wait() error {
	err := c.Cmd.Wait()
	c.logDone(c.startTime, err)
	return err
}

func (c *loggingCmd) logDone(start time.Time, err error) {
	if err != nil {
		c.log.Error(err, "DRBD command failed", "command", c.cmd, "args", c.args, "duration", time.Since(start).String())
	} else {
		c.log.Info("DRBD command complete", "command", c.cmd, "args", c.args, "duration", time.Since(start).String())
	}
}

func (c *loggingCmd) String() string {
	return c.Cmd.String()
}

// straceCmd wraps a drbdmeta call with strace, routing trace output through
// an os.Pipe directly into the structured log (no temp files).
type straceCmd struct {
	cmd        *exec.Cmd
	log        logr.Logger
	cmdName    string
	args       []string
	pipeReader *os.File
	pipeWriter *os.File
	startTime  time.Time
}

func newStraceCmd(ctx context.Context, logger logr.Logger, name string, arg ...string) *straceCmd {
	pr, pw, _ := os.Pipe()

	straceArgs := make([]string, 0, len(arg)+7)
	straceArgs = append(straceArgs, "-o", "/dev/fd/3", "-tt", "-T", "-f", name)
	straceArgs = append(straceArgs, arg...)

	cmd := exec.CommandContext(ctx, "strace", straceArgs...)
	cmd.ExtraFiles = []*os.File{pw} // fd 3 in child

	return &straceCmd{
		cmd: cmd, log: logger,
		cmdName: name, args: arg,
		pipeReader: pr, pipeWriter: pw,
	}
}

func (c *straceCmd) CombinedOutput() ([]byte, error) {
	c.log.Info("Executing DRBD command (strace)", "command", c.cmdName, "args", c.args)
	start := time.Now()

	out, err := c.cmd.CombinedOutput()
	c.pipeWriter.Close()
	straceOut, _ := io.ReadAll(c.pipeReader)
	c.pipeReader.Close()

	c.logDone(start, err, straceOut)
	return out, err
}

func (c *straceCmd) Start() error {
	c.log.Info("Executing DRBD command (strace)", "command", c.cmdName, "args", c.args)
	c.startTime = time.Now()
	return c.cmd.Start()
}

func (c *straceCmd) Wait() error {
	err := c.cmd.Wait()
	c.pipeWriter.Close()
	straceOut, _ := io.ReadAll(c.pipeReader)
	c.pipeReader.Close()

	c.logDone(c.startTime, err, straceOut)
	return err
}

func (c *straceCmd) logDone(start time.Time, err error, straceOut []byte) {
	duration := time.Since(start).String()
	trace := strings.TrimSpace(string(straceOut))
	if err != nil {
		c.log.Error(err, "DRBD command failed", "command", c.cmdName, "args", c.args, "duration", duration, "strace", trace)
	} else {
		c.log.Info("DRBD command complete", "command", c.cmdName, "args", c.args, "duration", duration, "strace", trace)
	}
}

func (c *straceCmd) StdoutPipe() (io.ReadCloser, error) { return c.cmd.StdoutPipe() }
func (c *straceCmd) String() string                     { return c.cmd.String() }
