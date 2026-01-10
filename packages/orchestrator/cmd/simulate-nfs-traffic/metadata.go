package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
)

type environmentMetadata struct {
	FilestoreCapacityGB  int
	FilestoreReadIOPS    int
	FilestoreMaxReadMBps int
	ClientMachineType    string
}

type filestoreInstance struct {
	FileShares []struct {
		CapacityGb string `json:"capacityGb"`
	} `json:"fileShares"`
	PerformanceLimits struct {
		MaxReadThroughputBytesPerSecond string `json:"maxReadThroughputBps"`
		MaxReadIOPS                     string `json:"maxReadIops"`
	} `json:"performanceLimits"`
}

func getEnvironmentMetadata(ctx context.Context, name, zone string) (environmentMetadata, error) {
	if name == "" {
		return environmentMetadata{}, nil
	}

	// get filestore metadata
	output, err := exec.CommandContext(ctx, "gcloud", "filestore", "instances", "describe", name, "--zone", zone, "--format=json").CombinedOutput()
	if err != nil {
		return environmentMetadata{}, fmt.Errorf("failed to get filestore metadata: %w", err)
	}

	var metadata filestoreInstance
	if err := json.Unmarshal(output, &metadata); err != nil {
		return environmentMetadata{}, fmt.Errorf("failed to unmarshal filestore metadata: %w", err)
	}

	machineType, err := getMetadata(ctx, "/computeMetadata/v1/instance/machine-type")
	if err != nil {
		return environmentMetadata{}, fmt.Errorf("failed to get machine type: %w", err)
	}

	return environmentMetadata{
		FilestoreCapacityGB:  mustParseInt(metadata.FileShares[0].CapacityGb),
		FilestoreReadIOPS:    mustParseInt(metadata.PerformanceLimits.MaxReadIOPS),
		FilestoreMaxReadMBps: mustParseInt(metadata.PerformanceLimits.MaxReadThroughputBytesPerSecond) / 1024 / 1024,
		ClientMachineType:    machineType,
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
