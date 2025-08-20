package envd

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestBindLocalhost(t *testing.T) {
	ctx := t.Context()

	client := setup.GetAPIClient()

	testCases := []struct {
		name         string
		bindAddress  string
		expectStatus int
	}{
		{
			name:         "bind_0_0_0_0",
			bindAddress:  "0.0.0.0",
			expectStatus: http.StatusOK,
		},
		{
			name:         "bind_::",
			bindAddress:  "::",
			expectStatus: http.StatusOK,
		},
		{
			name:         "bind_127_0_0_1",
			bindAddress:  "127.0.0.1",
			expectStatus: http.StatusOK,
		},
		{
			name:         "bind_localhost",
			bindAddress:  "localhost",
			expectStatus: http.StatusOK,
		},
		{
			name:         "bind_::1",
			bindAddress:  "::1",
			expectStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sbx := utils.SetupSandboxWithCleanup(t, client, utils.WithTimeout(300))
			envdClient := setup.GetEnvdClient(t, ctx)

			port := 3210

			serverCtx, serverCancel := context.WithCancel(ctx)
			defer serverCancel()

			go func() {
				err := utils.ExecCommand(t, serverCtx, sbx, envdClient, "python", "-m", "http.server", strconv.Itoa(port), "--bind", tc.bindAddress)
				if !errors.Is(err, context.Canceled) {
					require.NoError(t, err)
				}
			}()

			// Give the server time to start
			time.Sleep(5 * time.Second)

			baseURL, err := url.Parse(setup.EnvdProxy)
			require.NoError(t, err)

			httpClient := &http.Client{
				Timeout: 10 * time.Second,
			}

			req := utils.NewRequest(sbx, baseURL, port, nil)
			resp, err := httpClient.Do(req)
			require.NoErrorf(t, err, "Failed to connect to server bound to %s", tc.bindAddress)
			defer resp.Body.Close()

			assert.Equalf(t, tc.expectStatus, resp.StatusCode, "Unexpected status code %d for bind address %s", resp.StatusCode, tc.bindAddress)
		})
	}
}
