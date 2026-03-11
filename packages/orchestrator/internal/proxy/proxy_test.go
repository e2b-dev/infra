package proxy

import (
	"net/http"
	"testing"

	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
)

func TestContainsPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		ports []uint32
		port  uint64
		want  bool
	}{
		{"nil list", nil, 80, false},
		{"empty list", []uint32{}, 80, false},
		{"match", []uint32{80, 443}, 80, true},
		{"no match", []uint32{80, 443}, 8080, false},
		{"single match", []uint32{3000}, 3000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := containsPort(tt.ports, tt.port); got != tt.want {
				t.Errorf("containsPort(%v, %d) = %v, want %v", tt.ports, tt.port, got, tt.want)
			}
		})
	}
}

func TestExtractClientIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		clientIP   string // X-E2B-Client-IP header
		xff        string
		remoteAddr string
		want       string
	}{
		{
			name:       "X-E2B-Client-IP takes priority over everything",
			clientIP:   "203.0.113.42",
			xff:        "1.2.3.4",
			remoteAddr: "5.6.7.8:1234",
			want:       "203.0.113.42",
		},
		{
			name:       "X-Forwarded-For single IP",
			xff:        "1.2.3.4",
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For multiple IPs takes second-to-last",
			xff:        "1.2.3.4, 10.0.0.1, 172.16.0.1",
			remoteAddr: "5.6.7.8:1234",
			want:       "10.0.0.1",
		},
		{
			name:       "X-Forwarded-For two IPs takes first (second-to-last)",
			xff:        "  1.2.3.4  , 10.0.0.1",
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "no headers falls back to RemoteAddr",
			remoteAddr: "5.6.7.8:1234",
			want:       "5.6.7.8",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "5.6.7.8",
			want:       "5.6.7.8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{
				Header:     http.Header{},
				RemoteAddr: tt.remoteAddr,
			}
			if tt.clientIP != "" {
				r.Header.Set(reverseproxy.ClientIPHeader, tt.clientIP)
			}
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}

			if got := reverseproxy.ExtractClientIP(r); got != tt.want {
				t.Errorf("ExtractClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
