package drbdconfig_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/spf13/afero"
	"sigs.k8s.io/controller-runtime/pkg/client"

	drbdconfig "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbd_config"
)

type reconcileTestCase struct {
	name string
	//
	request                 drbdconfig.Request
	existingObjects         []client.Object
	fsSetup                 func()
	expectSharedSecretError *bool
	//
	assertErr func(*testing.T, error)
}

// SetFSForTests replaces filesystem for tests and returns a restore function.
// Production keeps OS-backed fs; tests swap it to memory/fs mocks.
func setupMemFS(t *testing.T) {
	t.Helper()
	prevAfs := drbdconfig.FS
	t.Cleanup(func() { drbdconfig.FS = prevAfs })
	drbdconfig.FS = &afero.Afero{Fs: afero.NewMemMapFs()}
}

func setupDiscardLogger(t *testing.T) {
	t.Helper()
	prevLogger := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
	})
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestReconciler_Reconcile(t *testing.T) {

}
