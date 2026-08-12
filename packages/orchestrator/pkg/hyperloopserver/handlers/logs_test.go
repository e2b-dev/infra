//go:build linux

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasStaleLogTimestamp(t *testing.T) {
	t.Parallel()

	lifecycleStart, err := time.Parse(envdTimestampLayout, "2026-07-16T10:00:00Z")
	require.NoError(t, err)

	t.Run("timestamp from the snapshot-restored clock is stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": "2026-06-09T08:00:00.123456789Z"}

		assert.True(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp after lifecycle start is not stale", func(t *testing.T) {
		t.Parallel()

		freshRaw := lifecycleStart.Add(time.Second).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": freshRaw}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp at lifecycle start is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": lifecycleStart.Format(envdTimestampLayout)}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp at the cutoff is not stale", func(t *testing.T) {
		t.Parallel()

		cutoffRaw := lifecycleStart.Add(-clockSkewTolerance).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": cutoffRaw}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp within clock-skew tolerance is not stale", func(t *testing.T) {
		t.Parallel()

		withinToleranceRaw := lifecycleStart.Add(-clockSkewTolerance / 2).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": withinToleranceRaw}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("timestamp past the cutoff is stale", func(t *testing.T) {
		t.Parallel()

		staleRaw := lifecycleStart.Add(-clockSkewTolerance - time.Second).Format(envdTimestampLayout)
		payload := map[string]any{"timestamp": staleRaw}

		assert.True(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("zero lifecycle start disables stale detection", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": "2026-06-09T08:00:00Z"}

		assert.False(t, hasStaleLogTimestamp(payload, time.Time{}))
	})

	t.Run("missing timestamp is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"message": "hello"}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("non-string timestamp is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": 12345}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})

	t.Run("invalid timestamp is not stale", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{"timestamp": "not-a-time"}

		assert.False(t, hasStaleLogTimestamp(payload, lifecycleStart))
	})
}

func TestAPIStoreForwardLogsStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusAccepted},
		{name: "client error", status: http.StatusUnprocessableEntity, wantErr: true},
		{name: "server error", status: http.StatusBadGateway, wantErr: true},
		{name: "rate limited", status: http.StatusTooManyRequests, wantErr: true},
		{name: "internal server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("collector diagnostic"))
			}))
			t.Cleanup(server.Close)

			store := &APIStore{collectorClient: *server.Client(), collectorAddr: server.URL}
			err := store.forwardLogs(t.Context(), []byte(`{"secret":"not-in-error"}`))
			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "not-in-error") {
				t.Fatalf("error contains request payload: %v", err)
			}
		})
	}
}

// TestAPIStoreForwardLogsZeroTimeoutRespectsClientTimeout is the legacy-mode
// equivalent: with timeout == 0 (route.Timeout unset, matching pre-flag
// behavior), forwardLogs must still be bounded by collectorClient's own
// Timeout rather than hanging forever.
func TestAPIStoreForwardLogsZeroTimeoutRespectsClientTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client := server.Client()
	client.Timeout = 50 * time.Millisecond
	store := &APIStore{collectorClient: *client, collectorAddr: server.URL}

	start := time.Now()
	err := store.forwardLogs(t.Context(), []byte(`{"msg":"slow"}`))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a slow/hung response once the client timeout passes")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("forwardLogs took %v to return; expected it to abort promptly once the 50ms client timeout passed", elapsed)
	}
}
