package port

import (
	"testing"

	"github.com/shirou/gopsutil/v4/net"
	"github.com/stretchr/testify/assert"
)

func TestScannerFilter_Match(t *testing.T) {
	t.Parallel()

	filter := &ScannerFilter{
		IPs:   []string{"127.0.0.1", "localhost", "::1", "::"},
		State: "LISTEN",
	}

	tests := []struct {
		name string
		conn net.ConnectionStat
		want bool
	}{
		{
			name: "IPv4 loopback matches",
			conn: net.ConnectionStat{Laddr: net.Addr{IP: "127.0.0.1"}, Status: "LISTEN"},
			want: true,
		},
		{
			name: "IPv6 loopback matches",
			conn: net.ConnectionStat{Laddr: net.Addr{IP: "::1"}, Status: "LISTEN"},
			want: true,
		},
		{
			name: "IPv6 wildcard matches",
			conn: net.ConnectionStat{Laddr: net.Addr{IP: "::"}, Status: "LISTEN"},
			want: true,
		},
		{
			name: "external IP does not match",
			conn: net.ConnectionStat{Laddr: net.Addr{IP: "10.0.0.1"}, Status: "LISTEN"},
			want: false,
		},
		{
			name: "wrong state does not match",
			conn: net.ConnectionStat{Laddr: net.Addr{IP: "127.0.0.1"}, Status: "ESTABLISHED"},
			want: false,
		},
		{
			name: "IPv4 wildcard (0.0.0.0) does not match",
			conn: net.ConnectionStat{Laddr: net.Addr{IP: "0.0.0.0"}, Status: "LISTEN"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, filter.Match(&tt.conn))
		})
	}
}
