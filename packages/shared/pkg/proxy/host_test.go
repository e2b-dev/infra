package proxy

import (
	"errors"
	"reflect"
	"testing"
)

func TestHostParser(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		wantID   string
		wantPort uint64
		wantErr  error
	}{
		{
			name:     "sandbox-host-with-client-id",
			host:     "49983-isv6ril5xadwn1k9t2jye-6532622b.e2b.app",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  nil,
		},
		{
			name:     "sandbox-host-with-client-id-and-dash-domain",
			host:     "49983-isv6ril5xadwn1k9t2jye-6532622b.e2b-test.app",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  nil,
		},
		{
			name:     "sandbox-host-with-client-id-and-subdomain",
			host:     "49983-isv6ril5xadwn1k9t2jye-6532622b.demo.e2b.app",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  nil,
		},
		{
			name:     "sandbox-host-without-client-id",
			host:     "49983-isv6ril5xadwn1k9t2jye.e2b.app",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  nil,
		},
		{
			name:     "sandbox-host-with-dash-domain-and-without-client-id",
			host:     "49983-isv6ril5xadwn1k9t2jye.e2b-test.app",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  nil,
		},
		{
			name:     "sandbox-host-with-subdomain-and-without-client-id",
			host:     "49983-isv6ril5xadwn1k9t2jye.demo.e2b.app",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  nil,
		},
		{
			name:     "sandbox-host-without-port-part",
			host:     "isv6ril5xadwn1k9t2jye.e2b.app",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  &InvalidHostError{},
		},
		{
			name:     "sandbox-host-with-invalid-port-part",
			host:     "abcd-isv6ril5xadwn1k9t2jye.e2b.app",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  &InvalidSandboxPortError{},
		},
		{
			name:     "sandbox-host-without-domain",
			host:     "49983-isv6ril5xadwn1k9t2jye",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  &InvalidHostError{},
		},
		{
			name:     "sandbox-host-with-missing-domain-and-port",
			host:     "49983-isv6ril5xadwn1k9t2jye:8080",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  &InvalidHostError{},
		},
		{
			name:     "sandbox-host-with-missing-domain",
			host:     "49983-isv6ril5xadwn1k9t2jye",
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 49983,
			wantErr:  &InvalidHostError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotPort, err := ParseHost(tt.host)

			// Compare error presence and, when present, the concrete type.
			if (err != nil) != (tt.wantErr != nil) {
				t.Fatalf("ParseHost(%q) error = %v, wantErr %v", tt.host, err, tt.wantErr)
			}

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) || reflect.TypeOf(err) != reflect.TypeOf(tt.wantErr) {
					t.Fatalf("ParseHost(%q) error type = %T, want %T", tt.host, err, tt.wantErr)
				}
				return // no further checks when an error was expected
			}

			if gotID != tt.wantID {
				t.Errorf("ParseHost(%q) sandboxID = %q, want %q", tt.host, gotID, tt.wantID)
			}

			if gotPort != tt.wantPort {
				t.Errorf("ParseHost(%q) port = %d, want %d", tt.host, gotPort, tt.wantPort)
			}
		})
	}
}
