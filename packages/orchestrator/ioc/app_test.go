package ioc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	e2bHealth "github.com/e2b-dev/infra/packages/shared/pkg/health"
)

func TestAppGraph(t *testing.T) {
	config, err := cfg.Parse()
	require.NoError(t, err)

	err = Validate(config, "version", "commit-sha")
	require.NoError(t, err)
}

func TestStartupShutdown(t *testing.T) {
	tempDir := t.TempDir()

	t.Setenv("ARTIFACTS_REGISTRY_PROVIDER", "Local")
	t.Setenv("BUILD_CACHE_BUCKET_NAME", "bucket-name")
	t.Setenv("CONSUL_TOKEN", "consul-token")
	t.Setenv("DUMP_GRAPH_DOT_FILE", "graph.dot")
	t.Setenv("ENVIRONMENT", "local")
	t.Setenv("GRPC_PORT", "28485")
	t.Setenv("NODE_ID", "testing-node-id")
	t.Setenv("ORCHESTRATOR_BASE_PATH", tempDir)
	t.Setenv("ORCHESTRATOR_SERVICES", "orchestrator,template-manager")
	t.Setenv("TEMPLATE_BUCKET_NAME", "bucket-name")
	t.Setenv("STORAGE_PROVIDER", "Local")
	t.Setenv("USE_LOCAL_NAMESPACE_STORAGE", "true")

	redisContainer, err := redis.Run(t.Context(), "redis:6")
	require.NoError(t, err)

	redisHost, err := redisContainer.Host(t.Context())
	require.NoError(t, err)
	redisPort, err := redisContainer.MappedPort(t.Context(), "6379")
	require.NoError(t, err)
	t.Setenv("REDIS_URL", fmt.Sprintf("%s:%d", redisHost, redisPort.Int()))

	config, err := cfg.Parse()
	require.NoError(t, err)

	// pull the factory out of the app so we can fake sandbox creation/destruction
	var sandboxFactory *sandbox.Factory
	app := New(config, "version", "commit-sha", fx.Invoke(func(f *sandbox.Factory) {
		sandboxFactory = f
	}))
	require.NotNil(t, app)

	err = app.Start(t.Context())
	require.NoError(t, err)

	// verify that health starting worked
	httpClient := &http.Client{}

	healthURL := fmt.Sprintf("http://localhost:%d/health", config.GRPCPort)
	healthReq, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		healthURL,
		nil,
	)
	require.NoError(t, err)

	waitForHttpResponse(t, httpClient, healthReq, time.Second*3, http.StatusOK, e2bHealth.Healthy)

	// pretend that we have started a sandbox
	sandboxFactory.AddSandbox()

	go func() {
		err = app.Stop(t.Context())
		assert.NoError(t, err)
	}()

	waitForHttpResponse(t, httpClient, healthReq, time.Second*3, http.StatusOK, e2bHealth.Draining)

	// stop the sandbox after ~1 second, to mimic a sandbox that needed time to shut down
	// do this async so that we can test for connection failures that happen _before_ the
	// sandbox was shutdown.
	var stopped bool
	go func() {
		time.Sleep(time.Second)
		sandboxFactory.SubtractSandbox()
		stopped = true
	}()

	// wait for the server to shut down
	waitForConnectionFailure(t, httpClient, healthReq, time.Second*30)

	// verify that the sandbox was stopped before the server went down
	assert.True(t, stopped)
}

func waitForConnectionFailure(t *testing.T, client *http.Client, req *http.Request, maxDuration time.Duration) {
	t.Helper()

	ctx := t.Context()
	ctx, cancel := context.WithTimeout(ctx, maxDuration)
	defer cancel()

	timer := time.NewTimer(maxDuration)

	for {
		select {
		case <-timer.C:
			t.Fatal("timed out waiting for http request")
		case <-ctx.Done():
			t.Fatalf("context done: %v", ctx.Err())
		default:
			break //nolint:revive,staticcheck // this is critical
		}

		resp, err := client.Do(req)
		if err == nil {
			err = resp.Body.Close()
			require.NoError(t, err)

			time.Sleep(time.Millisecond * 100)

			continue
		}

		if !errors.Is(err, syscall.ECONNREFUSED) {
			t.Logf("waiting for http connection to finish: %v", err.Error())
			time.Sleep(time.Millisecond * 100)
		}

		break
	}
}

func waitForHttpResponse(
	t *testing.T,
	httpClient *http.Client,
	request *http.Request,
	maxDuration time.Duration,
	expectedHTTPStatus int,
	expectedE2BStatus e2bHealth.Status,
) {
	t.Helper()

	ctx := t.Context()
	ctx, cancel := context.WithTimeout(ctx, maxDuration)
	defer cancel()

	timer := time.NewTimer(maxDuration)

	for {
		select {
		case <-timer.C:
			t.Fatal("timed out waiting for http request")
		case <-ctx.Done():
			t.Fatalf("context done: %v", ctx.Err())
		default:
		}
		resp, err := httpClient.Do(request.WithContext(ctx))
		require.NoError(t, err)

		if resp.StatusCode != expectedHTTPStatus {
			err = resp.Body.Close()
			require.NoError(t, err)

			t.Logf("expected http %d, got %d, waiting", expectedHTTPStatus, resp.StatusCode)
			time.Sleep(time.Millisecond * 100)

			continue
		}

		var model e2bHealth.Response
		err = json.NewDecoder(resp.Body).Decode(&model)
		require.NoError(t, err)

		err = resp.Body.Close()
		require.NoError(t, err)

		if model.Status != expectedE2BStatus {
			t.Logf("expected health status %s, got %s, waiting", expectedE2BStatus, model.Status)
			time.Sleep(time.Millisecond * 100)

			continue
		}

		break
	}
}
