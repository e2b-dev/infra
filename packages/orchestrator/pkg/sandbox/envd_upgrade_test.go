//go:build linux

package sandbox

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
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
