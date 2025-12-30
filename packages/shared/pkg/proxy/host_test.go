package proxy

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetTargetFromRequest(t *testing.T) { //nolint:tparallel // cannot call t.Setenv with t.Parallel
	t.Setenv("ENVIRONMENT", "local")

	getTargetFromRequest := GetTargetFromRequest(true)

	tests := []struct {
		name     string
		host     string
		headers  http.Header
		wantID   string
		wantPort uint64

		wantErrIs, wantErrAs error
	}{
		{
			name:      "sandbox-host-with-client-id",
			host:      "49983-isv6ril5xadwn1k9t2jye-6532622b.e2b.app",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: nil,
		},
		{
			name:      "sandbox-host-with-client-id-and-dash-domain",
			host:      "49983-isv6ril5xadwn1k9t2jye-6532622b.e2b-test.app",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: nil,
		},
		{
			name:      "sandbox-host-with-client-id-and-subdomain",
			host:      "49983-isv6ril5xadwn1k9t2jye-6532622b.demo.e2b.app",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: nil,
		},
		{
			name:      "sandbox-host-without-client-id",
			host:      "49983-isv6ril5xadwn1k9t2jye.e2b.app",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: nil,
		},
		{
			name:      "sandbox-host-with-dash-domain-and-without-client-id",
			host:      "49983-isv6ril5xadwn1k9t2jye.e2b-test.app",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: nil,
		},
		{
			name:      "sandbox-host-with-subdomain-and-without-client-id",
			host:      "49983-isv6ril5xadwn1k9t2jye.demo.e2b.app",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: nil,
		},
		{
			name:      "sandbox-host-without-port-part",
			host:      "isv6ril5xadwn1k9t2jye.e2b.app",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: ErrInvalidHost,
		},
		{
			name:      "sandbox-host-with-invalid-port-part",
			host:      "abcd-isv6ril5xadwn1k9t2jye.e2b.app",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrAs: InvalidSandboxPortError{},
		},
		{
			name:      "sandbox-host-without-domain",
			host:      "49983-isv6ril5xadwn1k9t2jye",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: ErrInvalidHost,
		},
		{
			name:      "sandbox-host-with-missing-domain-and-port",
			host:      "49983-isv6ril5xadwn1k9t2jye:8080",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: ErrInvalidHost,
		},
		{
			name:      "sandbox-host-with-missing-domain",
			host:      "49983-isv6ril5xadwn1k9t2jye",
			wantID:    "isv6ril5xadwn1k9t2jye",
			wantPort:  49983,
			wantErrIs: ErrInvalidHost,
		},
		{
			name: "headers: happy path",
			host: "localhost:1234",
			headers: http.Header{
				headerSandboxID:   []string{"isv6ril5xadwn1k9t2jye"},
				headerSandboxPort: []string{"8080"},
			},
			wantID:   "isv6ril5xadwn1k9t2jye",
			wantPort: 8080,
		},
		{
			name: "headers: missing sandbox id",
			host: "localhost:1234",
			headers: http.Header{
				headerSandboxPort: []string{"8080"},
			},
			wantErrIs: MissingHeaderError{Header: headerSandboxID},
		},
		{
			name: "headers: missing sandbox port",
			host: "localhost:1234",
			headers: http.Header{
				headerSandboxID: []string{"isv6ril5xadwn1k9t2jye"},
			},
			wantErrIs: MissingHeaderError{Header: headerSandboxPort},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &http.Request{
				Host:   tt.host,
				Header: tt.headers,
			}
			gotID, gotPort, err := getTargetFromRequest(req)

			// Compare error presence and, when present, the concrete type.
			if (err != nil) != (tt.wantErrIs != nil || tt.wantErrAs != nil) {
				t.Fatalf("ParseHost(%q) error = %v, wantErr %v", tt.host, err, tt.wantErrIs)
			}

			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)

				return // no further checks when an error was expected
			}

			if tt.wantErrAs != nil {
				require.ErrorAs(t, err, &tt.wantErrIs) //nolint:testifylint // doesn't need to

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
