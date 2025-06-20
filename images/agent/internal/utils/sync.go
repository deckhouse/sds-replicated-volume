package utils

import (
	"context"
	"fmt"
	"log/slog"
)

// Starts fn in a goroutine, which is expected to run forever (until error).
//
// Panics are recovered into errors.
//
// If fn returns nil error - [ErrUnexpectedReturnWithoutError] is returned.
//
// When error happens, it is passed to cancel, which is useful to cancel parent
// context.
func GoForever(
	goroutineName string,
	cancel context.CancelCauseFunc,
	log *slog.Logger,
	fn func() error,
) {
	log = log.With("goroutine", goroutineName)
	log.Info("starting")

	go func() {
		var err error

		defer func() {
			log.Info("stopped", "err", err)
		}()

		defer func() {
			cancel(fmt.Errorf("%s: %w", goroutineName, err))
		}()

		defer RecoverPanicToErr(&err)

		log.Info("started")

		err = fn()

		if err == nil {
			err = ErrUnexpectedReturnWithoutError
		}
	}()
}
