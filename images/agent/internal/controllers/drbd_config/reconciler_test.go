package drbdconfig_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"

	drbdconfig "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbd_config"
)

const testNodeName = "testNodeName"

type onRVUpdateTestCase struct {
	name string
	//
	oldRV, newRV *v1alpha3.ReplicatedVolume
	//
	expectedQueueItems []drbdconfig.Request
}

type onRVRCreateOrUpdateTestCase struct {
	name string
	//
	rvr *v1alpha3.ReplicatedVolumeReplica
	//
	expectedQueueItems []drbdconfig.Request
}

type reconcileTestCase struct {
	name string
	//
	request         drbdconfig.Request
	existingObjects []client.Object
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

func TestReconciler_OnRVUpdate(t *testing.T) {

	setupDiscardLogger(t)

	tests := []onRVUpdateTestCase{
		// TODO
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[drbdconfig.TReq]())
			defer q.ShutDown()

			rec := drbdconfig.NewReconciler(nil, nil, slog.Default(), testNodeName)

			rec.OnRVUpdate(t.Context(), tc.oldRV, tc.newRV, q)

			if got := q.Len(); got != len(tc.expectedQueueItems) {
				t.Fatalf("expected queue len %d, got %d", len(tc.expectedQueueItems), got)
			}
			for _, expectedReq := range tc.expectedQueueItems {
				item, _ := q.Get()

				if diff := cmp.Diff(expectedReq, item); diff != "" {
					t.Fatalf("mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestReconciler_OnRVRCreateOrUpdate(t *testing.T) {
	// TODO
}

func TestReconciler_Reconcile(t *testing.T) {
	// TODO
}
