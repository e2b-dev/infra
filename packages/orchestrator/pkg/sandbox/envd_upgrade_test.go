//go:build linux

package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsUpgradeDeliveryFailure guards the distinction CallEnvdUpgrade relies on:
// a request that never reached a running envd (so no upgrade happened) is a
// failure, while the expected post-send connection drop when envd execs
// mid-response is a success. Misclassifying the former as success would record a
// false upgrade in the rollout metrics.
func TestIsUpgradeDeliveryFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"connection refused -> failure", syscall.ECONNREFUSED, true},
		{"dialing connection refused -> failure", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"deadline exceeded -> not a delivery failure (ambiguous; confirmed by version)", context.DeadlineExceeded, false},
		{"dial timeout -> failure", &net.OpError{Op: "dial", Err: errors.New("i/o timeout")}, true},
		{"post-send reset -> success (envd exec'd)", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, false},
		{"EOF after body -> success (envd exec'd)", io.EOF, false},
		{"generic error -> success (assume exec'd)", errors.New("unexpected"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isUpgradeDeliveryFailure(tt.err))
		})
	}
}

func TestCallEnvdUpgrade_SetsBodySHA256Header(t *testing.T) {
	t.Parallel()

	payload := []byte("fake-envd-binary")
	wantSum := sha256.Sum256(payload)
	wantHex := hex.EncodeToString(wantSum[:])

	src := filepath.Join(t.TempDir(), "envd")
	require.NoError(t, os.WriteFile(src, payload, 0o755))

	var gotHex string
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/upgrade", r.URL.Path)
		gotHex = r.Header.Get("X-Envd-Upgrade-Sha256")
		gotLen = r.ContentLength
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, payload, body)
		// Simulate envd exec'ing without a response: close without writing.
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)

	token := "test-token"
	s := &Sandbox{Metadata: &Metadata{
		Config: &Config{
			Envd: EnvdMetadata{AccessToken: &token},
		},
	}}
	s.internalConfig.envdServerURLOverride = srv.URL

	_, err := s.CallEnvdUpgrade(t.Context(), src, "/usr/bin/envd.next", 5*time.Second)
	// Connection drop after body send is the expected success path.
	require.NoError(t, err)
	assert.Equal(t, wantHex, gotHex)
	assert.Equal(t, int64(len(payload)), gotLen)
}
