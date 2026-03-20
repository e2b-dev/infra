package proxy

import (
	"net/http"
	"testing"

	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
)

func TestExtractExternalClientIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		xff        string
		remoteAddr string
		want       string
	}{
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
		{
			name:       "ignores E2B header",
			xff:        "1.2.3.4",
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{
				Header:     http.Header{},
				RemoteAddr: tt.remoteAddr,
			}
			// Set E2B header to verify it's ignored
			r.Header.Set(reverseproxy.E2BClientIPHeader, "should-be-ignored")
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}

			if got := reverseproxy.ExtractExternalClientIP(r); got != tt.want {
				t.Errorf("ExtractExternalClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractE2BClientIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		clientIP string
		want     string
	}{
		{
			name:     "returns E2B header value",
			clientIP: "203.0.113.42",
			want:     "203.0.113.42",
		},
		{
			name: "missing header returns empty",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{Header: http.Header{}}
			if tt.clientIP != "" {
				r.Header.Set(reverseproxy.E2BClientIPHeader, tt.clientIP)
			}

			if got := reverseproxy.ExtractE2BClientIP(r); got != tt.want {
				t.Errorf("ExtractE2BClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
