package hotreload

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var HotReloadEnabledEnvVar = "HOTRELOAD_ENABLED"

type Option func(*Options)

type Options struct {
	// Only parent process will use this logger. Default is [slog.Default].
	Logger *slog.Logger

	// Will be used to wait for reload. Should block until either reload or
	// context cancelation. First call should return as soon as child process is
	// ready to be spawned.
	//
	// Default is [WithPeriodicalModtimeChecker(os.Args[0], time.Second)]
	WaitForChanges func(ctx context.Context)

	// Will be used to create a child command. By default, current process will
	// be launched with the same arguments and environment variables, with
	// stdout and stderr forwarding.
	// Command should consider ctx, i.e. spawned process should be killed on
	// cancelation.
	CreateCommand func(ctx context.Context) *exec.Cmd
}

func newOptions(opts ...Option) *Options {
	// defaults
	o := &Options{}
	WithLogger(slog.Default())(o)
	WithPeriodicalModtimeChecker(os.Args[0], time.Second)(o)
	WithCommand(func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], os.Args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		return cmd
	})(o)

	// overrides
	for _, fn := range opts {
		fn(o)
	}
	return o
}

func WithLogger(logger *slog.Logger) Option {
	return func(o *Options) {
		o.Logger = logger
	}
}

func WithPeriodicalModtimeChecker(filename string, period time.Duration) Option {
	return func(o *Options) {
		var lastModTime time.Time
		o.WaitForChanges = func(ctx context.Context) {
			for {
				stat, err := os.Stat(filename)

				if err != nil {
					o.Logger.Error(
						"hot reload error: os.Stat",
						"filename", filename,
						"err", err,
					)
				} else {
					modTime := stat.ModTime()
					if modTime != lastModTime {
						o.Logger.Debug(
							"change detected",
							"lastModTime", lastModTime,
							"modTime", modTime,
						)
						lastModTime = modTime
						return
					}
				}

				select {
				case <-ctx.Done():
					return
				case <-time.After(period):
				}
			}
		}
	}
}

func WithCommand(createCommand func(ctx context.Context) *exec.Cmd) Option {
	return func(o *Options) {
		o.CreateCommand = createCommand
	}
}

// support calling from `kubectl exec` in order to copy files into
// distroless container
func EnableCli() {
	if len(os.Args) >= 1 && os.Args[1] == "hotreload-cp" {
		if len(os.Args) == 2 {
			fmt.Println("Usage: hotreload-cp <target_path>")
			os.Exit(1)
		}

		gzipReader, err := gzip.NewReader(os.Stdin)
		if err != nil {
			fmt.Printf("creating gzip reader: %v", err)
			os.Exit(1)
		}
		defer gzipReader.Close()

		targetPath := os.Args[2]

		file, err := os.Create(targetPath)
		if err != nil {
			fmt.Printf("creating file: %v", err)
			os.Exit(1)
		}
		defer file.Close()

		_, err = io.Copy(file, gzipReader)
		if err != nil {
			fmt.Printf("writing to file: %v", err)
			os.Exit(1)
		}

		os.Exit(0)
	}
}

// Uses [HotReloadEnabledEnvVar] to determine if running in a parent process,
// which should run and then hot-reload child process. This function never
// returns for parent process. When context is canceled, child process is killed
// and [os.Exit] is called with the status of child process.
//
// Options can be provided directly, or using With* functions.
//
// Default behaviour is to "fork" current process and check for modtime each
// second for reloads.
func Enable(ctx context.Context, opts ...Option) {
	o := newOptions(opts...)

	// child process returns immediately
	if os.Getenv(HotReloadEnabledEnvVar) != "1" {
		return
	}

	// initial wait to start
	o.WaitForChanges(ctx)
	if ctx.Err() != nil {
		o.Logger.Info("exiting parent process before child process started")
		os.Exit(0)
	}

	for {
		if ctx.Err() != nil {
			o.Logger.Info("not starting reloading, because parent process is exiting")
			os.Exit(0)
		}

		childCtx, cancelChild := context.WithCancel(ctx)

		wg := &sync.WaitGroup{}
		wg.Add(1)

		beforeRetry := func() {
			cancelChild()
			wg.Wait()
			o.Logger.Info("retrying child process start in 1s...")
			time.Sleep(time.Second)
		}

		// detect changes in background
		var changeDetected bool
		go func() {
			defer wg.Done()

			o.WaitForChanges(childCtx)

			if ctx.Err() != nil {
				o.Logger.Info("stopped waiting for changes, because parent process is exiting")
				return
			}

			if childCtx.Err() != nil {
				o.Logger.Info("stopped waiting for changes, because child process exited")
				return
			}

			// change detected
			o.Logger.Info("change detected, reloading child process")
			changeDetected = true
			cancelChild()
		}()

		// start child process
		cmd := o.CreateCommand(childCtx)

		// prevent child process from forking itself
		cmd.Env = append(cmd.Env, HotReloadEnabledEnvVar+"=0")

		o.Logger.Info("starting child process", "cmd", cmd.String())

		if err := cmd.Start(); err != nil {
			o.Logger.Error("error during Start of child process", "err", err)
			// retry
			beforeRetry()
			continue
		}
		o.Logger.Info("child process started", "pid", cmd.Process.Pid)

		var childExitErr *exec.ExitError
		if err := cmd.Wait(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				childExitErr = exitErr
				o.Logger.Info("child process exited/killed", "exitCode", exitErr.ExitCode())
			} else if !errors.Is(err, context.Canceled) {
				o.Logger.Error("error during Wait of child process", "err", err)
				// retry
				beforeRetry()
				continue
			} else {
				o.Logger.Info("child process canceled")
			}
		} else {
			o.Logger.Info("child process exited successfully")
		}

		cancelChild()
		wg.Wait()

		var exiting bool

		if ctx.Err() != nil {
			o.Logger.Info("stopped reloading, because parent process is exiting")
			exiting = true

		} else if !changeDetected {
			o.Logger.Info("stopped reloading, because child process exited/killed")
			exiting = true
		}

		if exiting {
			if childExitErr == nil || childExitErr.Exited() {
				var code int
				if childExitErr != nil {
					code = childExitErr.ExitCode()
				}
				o.Logger.Info("exiting with child exit code", "code", code)
				os.Exit(code)
			}

			var signal int
			if ws, ok := childExitErr.Sys().(syscall.WaitStatus); ok {
				signal = int(ws.Signal())
			} else {
				// problematic case (not on Unix?)
			}

			o.Logger.Warn("child process was killed with signal", "signal", signal)

			// mimic typical shell behavior, when child process dies from a
			// a signal
			os.Exit(128 + signal)
		}
	}
}
