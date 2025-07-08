package hotreload

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

type Options struct {
	// Only parent process will use this logger. Default is [slog.Default].
	Logger *slog.Logger

	// Will be used to wait for reload. First call should return as soon as
	// child process is ready to be spawned.
	//
	// Default is [WithPeriodicalModtimeChecker(os.Args[0], time.Second)]
	WaitForChanges func(ctx context.Context)

	// Will be used to create a child command. By default, current process will
	// be launched with the same arguments and environment variables, with
	// stdout and stderr forwarding.
	CreateCommand func() *exec.Cmd
}

const HotReloadEnabledEnvVar = "HOT_RELOAD_ENABLED"

type Option func(*Options)

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

func WithCommand(createCommand func() *exec.Cmd) Option {
	return func(o *Options) {
		o.CreateCommand = createCommand
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
	// child process returns immediately
	if os.Getenv(HotReloadEnabledEnvVar) == "1" {
		return
	}

	o := &Options{}

	// defaults
	WithLogger(slog.Default())
	WithPeriodicalModtimeChecker(os.Args[0], time.Second)
	WithCommand(func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], os.Args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		return cmd
	})

	// overrides
	for _, fn := range opts {
		fn(o)
	}

	var cmd *exec.Cmd

	killChild := func() *os.ProcessState {
		err := cmd.Process.Kill()
		if err != nil {
			o.Logger.Error("error during Kill of child process", "err", err)
		}

		state, err := cmd.Process.Wait()
		if err != nil {
			o.Logger.Error("error during Wait of child process", "err", err)
		}
		return state
	}

	for {
		o.WaitForChanges(ctx)
		if ctx.Err() != nil {
			if cmd == nil || cmd.Process == nil {
				o.Logger.Info("exiting parent process before child process started")
				os.Exit(0)
			}

			state := killChild()

			o.Logger.Info(
				"exiting parent process",
				"exitCode", state.ExitCode(),
				"ctxErr", ctx.Err(),
			)
			os.Exit(state.ExitCode())
		}

		if cmd != nil {
			// terminate old child process
			state := killChild()
			o.Logger.Info(
				"old child process killed",
				"exitCode", state.ExitCode(),
			)
		}

		// spawn new child process
		cmd = o.CreateCommand()

		// prevent child process from forking itself
		cmd.Env = append(cmd.Env, HotReloadEnabledEnvVar+"=0")

		if err := cmd.Start(); err != nil {
			o.Logger.Error("error during Start of child process", "err", err)
		}
	}
}
