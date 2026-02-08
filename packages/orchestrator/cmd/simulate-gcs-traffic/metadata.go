package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type environmentMetadata struct {
	ClientMachineType string
}

func getEnvironmentMetadata(ctx context.Context) (environmentMetadata, error) {
	machineType, err := getMetadata(ctx, "/computeMetadata/v1/instance/machine-type")
	if err != nil {
		return environmentMetadata{}, fmt.Errorf("failed to get machine type: %w", err)
	}

	return environmentMetadata{
		ClientMachineType: machineType,
	}, nil
}

var client http.Client

func getMetadata(ctx context.Context, path string) (string, error) {
	path = strings.TrimPrefix(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://metadata.google.internal/%s", path), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header = map[string][]string{"Metadata-Flavor": {"Google"}}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get response: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	output, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	text := strings.TrimSpace(string(output))
	parts := strings.Split(text, "/")

	return parts[len(parts)-1], nil
}
