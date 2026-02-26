package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

type Snapshot struct {
	SandboxIDs []string `json:"sandbox_ids"`
	TakenAt    string   `json:"taken_at"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <snapshot|check> <snapshot_file>\n", os.Args[0])
		os.Exit(2)
	}

	command := os.Args[1]
	snapshotPath := os.Args[2]

	switch command {
	case "snapshot":
		if err := snapshot(snapshotPath); err != nil {
			fmt.Fprintf(os.Stderr, "failed to snapshot sandboxes: %v\n", err)
			os.Exit(1)
		}
	case "check":
		if err := check(snapshotPath); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox leak check failed: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
		os.Exit(2)
	}
}

func snapshot(snapshotPath string) error {
	sandboxes, err := listSandboxes()
	if err != nil {
		return err
	}

	ids := sandboxIDs(sandboxes)
	sort.Strings(ids)

	snapshotData := Snapshot{
		SandboxIDs: ids,
		TakenAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	raw, err := json.Marshal(snapshotData)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	if err := os.WriteFile(snapshotPath, raw, 0o600); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}

	fmt.Printf("Sandbox leak snapshot saved: %d sandboxes\n", len(ids))

	return nil
}

func check(snapshotPath string) error {
	raw, err := os.ReadFile(snapshotPath)
	if err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}

	var snapshotData Snapshot
	if err := json.Unmarshal(raw, &snapshotData); err != nil {
		return fmt.Errorf("unmarshal snapshot: %w", err)
	}

	currentSandboxes, err := listSandboxes()
	if err != nil {
		return err
	}

	baseline := make(map[string]struct{}, len(snapshotData.SandboxIDs))
	for _, id := range snapshotData.SandboxIDs {
		baseline[id] = struct{}{}
	}

	var leaked []api.ListedSandbox
	for _, sbx := range currentSandboxes {
		if _, exists := baseline[sbx.SandboxID]; !exists {
			leaked = append(leaked, sbx)
		}
	}

	if len(leaked) == 0 {
		fmt.Printf(
			"Sandbox leak check passed: baseline=%d current=%d leaked=0\n",
			len(snapshotData.SandboxIDs),
			len(currentSandboxes),
		)
		return nil
	}

	sort.Slice(leaked, func(i, j int) bool {
		return leaked[i].SandboxID < leaked[j].SandboxID
	})

	fmt.Printf(
		"Detected leaked sandboxes: baseline=%d current=%d leaked=%d\n",
		len(snapshotData.SandboxIDs),
		len(currentSandboxes),
		len(leaked),
	)
	for _, sbx := range leaked {
		metadata := "{}"
		if sbx.Metadata != nil {
			if rawMetadata, err := json.Marshal(*sbx.Metadata); err == nil {
				metadata = string(rawMetadata)
			}
		}

		fmt.Printf(
			"- sandboxID=%s state=%s startedAt=%s metadata=%s\n",
			sbx.SandboxID,
			sbx.State,
			sbx.StartedAt.Format(time.RFC3339),
			metadata,
		)
	}

	return fmt.Errorf("sandbox leaks detected")
}

func listSandboxes() ([]api.ListedSandbox, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := setup.GetAPIClient()
	response, err := client.GetV2SandboxesWithResponse(ctx, &api.GetV2SandboxesParams{}, setup.WithAPIKey())
	if err != nil {
		return nil, fmt.Errorf("get sandboxes: %w", err)
	}

	if response.JSON200 == nil {
		return nil, fmt.Errorf("unexpected response from GET /v2/sandboxes: status=%d body=%s", response.StatusCode(), string(response.Body))
	}

	return *response.JSON200, nil
}

func sandboxIDs(sandboxes []api.ListedSandbox) []string {
	ids := make([]string, 0, len(sandboxes))
	for _, sbx := range sandboxes {
		ids = append(ids, sbx.SandboxID)
	}

	return ids
}
