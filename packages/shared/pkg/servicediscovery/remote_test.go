package servicediscovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Ported with the backend from the api's clusters/discovery package.
func TestHostWithoutPort(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct{ in, want string }{
		"host with port":      {"10.0.0.12:5008", "10.0.0.12"},
		"dns host with port":  {"orch-1.internal:5008", "orch-1.internal"},
		"host without port":   {"10.0.0.12", "10.0.0.12"},
		"ipv6 host with port": {"[fd00::1]:5008", "fd00::1"},
		"empty host":          {" ", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, hostWithoutPort(tt.in))
		})
	}
}
