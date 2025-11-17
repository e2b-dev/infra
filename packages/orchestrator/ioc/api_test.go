package ioc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	e2bHealth "github.com/e2b-dev/infra/packages/shared/pkg/health"
)

type shutdown struct {
	isShutdown atomic.Bool
}

var _ fx.Shutdowner = (*shutdown)(nil)

func (s *shutdown) Shutdown(...fx.ShutdownOption) error {
	s.isShutdown.Store(true)

	return nil
}

func TestCanCloseGRPCGracefully(t *testing.T) {
	logger := zap.L()

	var sandboxFactory sync.WaitGroup

	httpClient := &http.Client{}
	shutdowner := &shutdown{}
	grpcServer := grpc.NewServer()
	config := cfg.Config{GRPCPort: 49394}
	version := VersionInfo{}
	state := State{}
	serviceInfo := NewServiceInfo(state, config, version)
	healthHttpServer, err := newHealthHTTPServer(serviceInfo)
	require.NoError(t, err)

	healthURL := fmt.Sprintf("http://localhost:%d/health", config.GRPCPort)
	healthReq, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		healthURL,
		nil,
	)
	require.NoError(t, err)

	output, err := newCMUXServer(config, logger, nil, shutdowner, grpcServer, healthHttpServer, serviceInfo)
	require.NoError(t, err)

	startCMUXServer(logger, shutdowner, output, grpcServer, healthHttpServer)

	// verify that health starting worked
	waitForHttpResponse(t, httpClient, healthReq, time.Second*3, http.StatusOK, e2bHealth.Healthy)

	// pretend that we have started a sandbox
	sandboxFactory.Add(1)

	go stopCMUXServerMockable(
		logger,
		&sandboxFactory,
		output,
		grpcServer,
		healthHttpServer,
		serviceInfo,
	)

	waitForHttpResponse(t, httpClient, healthReq, time.Second*3, http.StatusOK, e2bHealth.Draining)

	// stop the sandbox after ~1 second, to mimic a sandbox that needed time to shut down
	// do this async so that we can test for connection failures that happen _before_ the
	// sandbox was shutdown.
	var stopped bool
	go func() {
		time.Sleep(time.Second)
		sandboxFactory.Done()
		stopped = true
	}()

	// wait for the server to shut down
	waitForConnectionFailure(t, httpClient, healthReq, time.Second*3)

	// verify that the sandbox was stopped before the server went down
	assert.True(t, stopped)

	assert.True(t, shutdowner.isShutdown.Load())
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
		resp, err := httpClient.Do(request)
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
