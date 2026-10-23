//go:build linux

package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/sandboxtypes"
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

// A full-router sandbox is refused even with a valid traffic access token,
// and no connection reaches the guest. Egress-router still reaches it.
func TestSandboxProxyRefusesFullRouterIngress(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		placement *orchestrator.SandboxNetworkPlacement
		refused   bool
	}{
		{name: "egress-router", placement: &orchestrator.SandboxNetworkPlacement{ExecutionId: "execution", NetworkMode: "egress-router", RouterId: "router"}},
		{name: "full-router", placement: &orchestrator.SandboxNetworkPlacement{ExecutionId: "execution", NetworkMode: "full-router", RouterId: "router", IngressAddress: "127.0.0.1", IngressPort: 5010}, refused: true},
		{name: "full-router without its Router", placement: &orchestrator.SandboxNetworkPlacement{NetworkMode: "full-router"}, refused: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var accepted atomic.Int32
			guest := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("guest"))
			}))
			guest.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					accepted.Add(1)
				}
			}
			guest.Start()
			defer guest.Close()
			address, err := url.Parse(guest.URL)
			require.NoError(t, err)

			sandboxes := sandbox.NewSandboxesMap()
			proxy := reverseproxy.New(0, 1, time.Minute, newDestinationResolver(sandboxes), nil, true)
			defer proxy.Close()
			frontend := httptest.NewServer(proxy.Handler)
			defer frontend.Close()

			token := "test-traffic-token"
			sbx := &sandbox.Sandbox{
				LifecycleID: "lifetime",
				Metadata: &sandbox.Metadata{
					Config:  sandbox.NewConfig(sandbox.Config{Network: &orchestrator.SandboxNetworkConfig{Ingress: &orchestrator.SandboxNetworkIngressConfig{TrafficAccessToken: &token}}}),
					Runtime: sandboxtypes.RuntimeMetadata{SandboxID: testSandboxID, ExecutionID: "execution"},
				},
				Resources:        &sandbox.Resources{Slot: &network.Slot{HostIP: net.ParseIP("127.0.0.1")}},
				NetworkPlacement: tt.placement,
			}
			require.NoError(t, sandboxes.MarkRunning(t.Context(), sbx))

			r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, frontend.URL, nil)
			require.NoError(t, err)
			r.Host = fmt.Sprintf("%s-%s.e2b.app", address.Port(), testSandboxID)
			r.Header.Set("e2b-traffic-access-token", token)
			response, err := http.DefaultClient.Do(r)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())

			if !tt.refused {
				require.Equal(t, http.StatusOK, response.StatusCode)
				assert.Equal(t, "guest", string(body))
				assert.EqualValues(t, 1, accepted.Load())

				return
			}
			require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
			assert.Contains(t, string(body), `"error_code":"`+proxygrpc.PlacementFeatureUnsupported+`"`)
			assert.Empty(t, response.Header.Get("Retry-After"))
			assert.Zero(t, accepted.Load(), "no connection reaches the guest")
		})
	}
}
