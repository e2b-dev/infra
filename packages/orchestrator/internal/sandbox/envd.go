package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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

	request, err := http.NewRequestWithContext(childCtx, "POST", address, bytes.NewReader(envVarsJSON))
	if err != nil {
		return err
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}

	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", response.StatusCode)
	}

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return err
	}

	defer response.Body.Close()

	return nil
}
