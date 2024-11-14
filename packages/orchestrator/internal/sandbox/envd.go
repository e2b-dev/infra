package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func (s *Sandbox) syncOldEnvd(ctx context.Context) error {
	address := fmt.Sprintf("http://%s:%d/sync", s.slot.HostIP(), consts.OldEnvdServerPort)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		request, err := http.NewRequestWithContext(ctx, "POST", address, nil)
		if err != nil {
			return err
		}

		response, err := httpClient.Do(request)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to send sync request to old envd: %v\n", err)

			time.Sleep(10 * time.Millisecond)

			continue
		}

		_, err = io.Copy(io.Discard, response.Body)
		if err != nil {
			return err
		}

		err = response.Body.Close()
		if err != nil {
			return err
		}

		return nil
	}
}

type PostInitJSONBody struct {
	EnvVars *map[string]string `json:"envVars"`
}

func (s *Sandbox) initEnvd(ctx context.Context, tracer trace.Tracer, envVars map[string]string) error {
	childCtx, childSpan := tracer.Start(ctx, "envd-init")
	defer childSpan.End()

	address := fmt.Sprintf("http://%s:%d/init", s.slot.HostIP(), consts.DefaultEnvdServerPort)

	jsonBody := &PostInitJSONBody{
		EnvVars: &envVars,
	}

	envVarsJSON, err := json.Marshal(jsonBody)
	if err != nil {
		return err
	}
	counter := 0

	for {
		select {
		case <-childCtx.Done():
			return childCtx.Err()
		default:
		}

		request, err := http.NewRequestWithContext(childCtx, "POST", address, bytes.NewReader(envVarsJSON))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		response, err := httpClient.Do(request)
		if err != nil {
			counter++
			if counter%10 == 0 {
				log.Printf("[%dth try] failed to send sync request to new envd: %v\n", counter+1, err)
			}

			time.Sleep(10 * time.Millisecond)

			continue
		}

		if response.StatusCode != http.StatusNoContent {
			return fmt.Errorf("unexpected status code: %d", response.StatusCode)
		}

		_, err = io.Copy(io.Discard, response.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}

		err = response.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to close response body: %w", err)
		}

		return nil
	}
}
