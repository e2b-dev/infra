package proxy

import (
	"net/http"
	"testing"
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
			name:       "X-Forwarded-For multiple IPs takes first",
			xff:        "1.2.3.4, 10.0.0.1, 172.16.0.1",
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For with spaces",
			xff:        "  1.2.3.4  , 10.0.0.1",
			remoteAddr: "5.6.7.8:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "no XFF falls back to RemoteAddr",
			xff:        "",
			remoteAddr: "5.6.7.8:1234",
			want:       "5.6.7.8",
		},
		{
			name:       "no XFF RemoteAddr without port",
			xff:        "",
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
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}

			if got := extractClientIP(r); got != tt.want {
				t.Errorf("extractClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
