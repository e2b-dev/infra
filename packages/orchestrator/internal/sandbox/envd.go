package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"go.opentelemetry.io/otel/trace"
)

func (s *Sandbox) syncOldEnvd(ctx context.Context) error {
	address := fmt.Sprintf("http://%s:%d/sync", s.slot.HostIP(), consts.OldEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "POST", address, nil)
	if err != nil {
		return err
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return err
	}

	defer response.Body.Close()

	return nil
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

	for i := 0; i < 10; i++ {
		time.Sleep(10 * time.Millisecond)
		request, err := http.NewRequestWithContext(childCtx, "POST", address, bytes.NewReader(envVarsJSON))
		if err != nil {
			fmt.Printf("failed to create request: %v\n", err)
			continue
		}

		response, err := httpClient.Do(request)
		if err != nil {
			fmt.Printf("failed to send request: %v\n", err)
			continue
		}

		if response.StatusCode != http.StatusNoContent {
			fmt.Printf("unexpected status code: %d\n", response.StatusCode)
			continue
		}

		_, err = io.Copy(io.Discard, response.Body)
		if err != nil {
			fmt.Printf("failed to read response body: %v\n", err)
			continue
		}

		err = response.Body.Close()
		if err != nil {
			fmt.Printf("failed to close response body: %v\n", err)
			continue
		}

		return nil
	}

	return fmt.Errorf("failed to init envd")
}
