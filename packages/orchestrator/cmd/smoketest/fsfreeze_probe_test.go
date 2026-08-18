//go:build linux

package smoketest

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// freezableEnvd is the smallest server that behaves like envd on a filesystem
// that can be frozen: writes block while frozen and are released by the thaw,
// reads always answer.
type freezableEnvd struct {
	mu     sync.Mutex
	frozen bool
	thawed chan struct{}
}

func newFreezableEnvd() *freezableEnvd {
	return &freezableEnvd{thawed: make(chan struct{})}
}

func (f *freezableEnvd) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/fsfreeze":
		f.mu.Lock()
		f.frozen = true
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	case r.URL.Path == "/fsthaw":
		f.mu.Lock()
		wasFrozen := f.frozen
		f.frozen = false
		f.mu.Unlock()

		if wasFrozen {
			close(f.thawed)
		}

		w.WriteHeader(http.StatusNoContent)

	case r.URL.Path == "/files" && r.Method == http.MethodPost:
		f.mu.Lock()
		frozen := f.frozen
		f.mu.Unlock()

		if frozen {
			// A write to a frozen filesystem blocks until the thaw.
			select {
			case <-f.thawed:
			case <-r.Context().Done():
				return
			}
		}

		w.WriteHeader(http.StatusOK)

	case r.URL.Path == "/files" && r.Method == http.MethodGet:
		// Reads are unaffected by the freeze.
		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// TestFsFreezeProbeDetectsAWorkingFreeze pins the control flow of the probe that
// TestSmokeAllFCVersions runs against a live guest: a write that never returns is
// the pass condition, it has to be collected after the thaw, and nothing may be
// left blocked behind.
//
// This tests the probe, not envd — the server here blocks because it is told to.
// The claim about FIFREEZE is made by the smoke test, which runs this same probe
// where only a real freeze can satisfy it, and by
// TestFreezeBlocksWritesAndThawReleasesThem in packages/envd. It exists because
// the smoke test needs root, KVM and a template build, so the probe would
// otherwise go unexercised on every machine that cannot run it.
func TestFsFreezeProbeDetectsAWorkingFreeze(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newFreezableEnvd())
	t.Cleanup(server.Close)

	assertFsFreezeQuiescesRootfsAt(t, t.Context(), server.URL, "probe-token")
}
