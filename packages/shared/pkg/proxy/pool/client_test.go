package pool

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamErrorStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "connection refused",
			err:  syscall.ECONNREFUSED,
			want: http.StatusServiceUnavailable,
		},
		{
			name: "wrapped connection reset",
			err:  errors.Join(errors.New("proxy read failed"), syscall.ECONNRESET),
			want: http.StatusServiceUnavailable,
		},
		{
			name: "net error",
			err:  &net.DNSError{Err: "lookup failed"},
			want: http.StatusServiceUnavailable,
		},
		{
			name: "client canceled",
			err:  context.Canceled,
			want: http.StatusBadGateway,
		},
		{
			name: "unknown error",
			err:  errors.New("malformed upstream response"),
			want: http.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, upstreamErrorStatus(tt.err))
		})
	}
}
