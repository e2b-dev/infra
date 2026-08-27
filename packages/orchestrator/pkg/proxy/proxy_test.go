//go:build linux

package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
)

const testSandboxID = "im9r2ycjiy2534qsdy1oo"

// resolve routes a request for the given sandbox port and path through the
// proxy's destination resolver against an empty sandbox map, which is all the
// internal-route refusal needs: it is decided before the sandbox is looked up.
func resolve(t *testing.T, port int64, path string) error {
	t.Helper()

	resolveDestination := newDestinationResolver(sandbox.NewSandboxesMap())

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil)
	r.Host = fmt.Sprintf("%d-%s.e2b.app", port, testSandboxID)

	_, err := resolveDestination(r)
	require.Error(t, err, "an empty sandbox map cannot resolve a destination")

	return err
}

// TestResolveDestinationRefusesInternalEnvdRoutes covers the wiring: envd's
// control plane is refused on the envd port, and only there.
func TestResolveDestinationRefusesInternalEnvdRoutes(t *testing.T) {
	t.Parallel()

	const userPort = 8000

	for _, tt := range []struct {
		name    string
		port    int64
		path    string
		refused bool
	}{
		{name: "init on the envd port", port: consts.DefaultEnvdServerPort, path: "/init", refused: true},
		{name: "freeze on the envd port", port: consts.DefaultEnvdServerPort, path: "/freeze", refused: true},
		{name: "upgrade on the envd port", port: consts.DefaultEnvdServerPort, path: "/upgrade", refused: true},

		// The public envd surface is untouched.
		{name: "files on the envd port", port: consts.DefaultEnvdServerPort, path: "/files", refused: false},
		{name: "health on the envd port", port: consts.DefaultEnvdServerPort, path: "/health", refused: false},

		// A user process may serve whatever it likes on its own ports; envd's
		// control plane does not live there and the name is not reserved.
		{name: "init on a user port", port: userPort, path: "/init", refused: false},
		{name: "upgrade on a user port", port: userPort, path: "/upgrade", refused: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := resolve(t, tt.port, tt.path)

			var internalRouteErr *reverseproxy.InternalRouteError
			if !tt.refused {
				assert.NotErrorAs(t, err, &internalRouteErr)

				return
			}

			require.ErrorAs(t, err, &internalRouteErr)
			assert.Equal(t, tt.path, internalRouteErr.Path)
			assert.Equal(t, testSandboxID, internalRouteErr.SandboxId)
		})
	}
}

// TestResolveDestinationRefusesInternalRoutesBeforeSandboxLookup pins the
// ordering the refusal relies on: a request for a control route is answered the
// same way whether or not the sandbox is on this node, so the reply reveals
// nothing about which sandboxes are here.
func TestResolveDestinationRefusesInternalRoutesBeforeSandboxLookup(t *testing.T) {
	t.Parallel()

	var notFoundErr *reverseproxy.SandboxNotFoundError

	err := resolve(t, consts.DefaultEnvdServerPort, "/init")
	assert.NotErrorAs(t, err, &notFoundErr, "the refusal must not depend on the sandbox being absent")

	err = resolve(t, consts.DefaultEnvdServerPort, "/files")
	assert.ErrorAs(t, err, &notFoundErr, "a public route on an absent sandbox still reports the sandbox missing")
}

func TestSchemeForPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ingress *orchestrator.SandboxNetworkIngressConfig
		port    uint64
		want    string
	}{
		{
			name:    "nil ingress defaults to http",
			ingress: nil,
			port:    8080,
			want:    "http",
		},
		{
			name:    "no HTTPS ports defaults to http",
			ingress: &orchestrator.SandboxNetworkIngressConfig{},
			port:    8080,
			want:    "http",
		},
		{
			name:    "port in HTTPS ports uses https",
			ingress: &orchestrator.SandboxNetworkIngressConfig{HttpsPorts: []uint32{443, 8443}},
			port:    8443,
			want:    "https",
		},
		{
			name:    "port not in HTTPS ports uses http",
			ingress: &orchestrator.SandboxNetworkIngressConfig{HttpsPorts: []uint32{443, 8443}},
			port:    8080,
			want:    "http",
		},
		{
			name:    "envd port stays http even when listed",
			ingress: &orchestrator.SandboxNetworkIngressConfig{HttpsPorts: []uint32{uint32(consts.DefaultEnvdServerPort)}},
			port:    uint64(consts.DefaultEnvdServerPort),
			want:    "http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, schemeForPort(tt.ingress, tt.port))
		})
	}
}
