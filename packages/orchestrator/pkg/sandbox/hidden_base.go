package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	prepareBaseTimeout      = 30 * time.Second
	hiddenBaseHealthTimeout = 30 * time.Second
	hiddenBaseHealthDelay   = 50 * time.Millisecond
	fsResumeTimeout         = 10 * time.Second
	fsSyncTimeout           = 10 * time.Second
)

// requestEnvdPrepareBase tells the guest agent to set up a tmpfs root and
// call systemctl switch-root. After this call returns, the orchestrator
// must poll /health until the new envd (hidden-base mode) is ready.
func (s *Sandbox) requestEnvdPrepareBase(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "envd-prepare-base")
	defer span.End()

	address := fmt.Sprintf("http://%s:%d/fs-snapshot/prepare-base", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	reqCtx, cancel := context.WithTimeout(ctx, prepareBaseTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, address, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := sandboxHttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("prepare-base request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("prepare-base returned %d: %s", resp.StatusCode, string(body))
	}

	telemetry.ReportEvent(ctx, "envd prepare-base request accepted")

	return nil
}

// waitForHiddenBaseReady polls the envd health endpoint until the new
// hidden-base mode server is up (after switch-root).
func (s *Sandbox) waitForHiddenBaseReady(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "wait-hidden-base-ready")
	defer span.End()

	address := fmt.Sprintf("http://%s:%d/health", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	deadline := time.After(hiddenBaseHealthTimeout)

	for {
		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, address, nil)
		if err != nil {
			cancel()
			return fmt.Errorf("failed to create health request: %w", err)
		}

		resp, err := sandboxHttpClient.Do(req)
		cancel()

		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
				telemetry.ReportEvent(ctx, "hidden-base envd is ready")
				return nil
			}
		}

		select {
		case <-deadline:
			return fmt.Errorf("timeout waiting for hidden-base envd to become ready")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(hiddenBaseHealthDelay):
		}
	}
}

// requestEnvdFSResume tells the guest agent (running in hidden-base mode)
// to mount /dev/vda and pivot_root into the ext4 rootfs.
func (s *Sandbox) requestEnvdFSResume(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "envd-fs-resume")
	defer span.End()

	address := fmt.Sprintf("http://%s:%d/fs-snapshot/resume", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	reqCtx, cancel := context.WithTimeout(ctx, fsResumeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, address, nil)
	if err != nil {
		return fmt.Errorf("failed to create resume request: %w", err)
	}

	resp, err := sandboxHttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fs-resume request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fs-resume returned %d: %s", resp.StatusCode, string(body))
	}

	telemetry.ReportEvent(ctx, "envd fs-resume completed")

	return nil
}

// requestEnvdSync tells the guest agent to flush all pending filesystem
// writes to disk. Used before FS-only pause to ensure the CoW diff
// captures a consistent filesystem state.
func (s *Sandbox) requestEnvdSync(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "envd-fs-sync")
	defer span.End()

	address := fmt.Sprintf("http://%s:%d/fs-snapshot/sync", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	reqCtx, cancel := context.WithTimeout(ctx, fsSyncTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, address, nil)
	if err != nil {
		return fmt.Errorf("failed to create sync request: %w", err)
	}

	resp, err := sandboxHttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sync request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sync returned %d: %s", resp.StatusCode, string(body))
	}

	telemetry.ReportEvent(ctx, "envd filesystem synced")

	return nil
}

// CreateHiddenBase takes a running sandbox and creates a hidden base
// snapshot with ext4 unmounted. The snapshot captures kernel memory,
// network state, and the agent running from tmpfs.
//
// Flow:
//  1. Tell envd to prepare (switch-root to tmpfs)
//  2. Wait for hidden-base envd to be ready
//  3. Pause VM
//  4. Take FC snapshot → hidden base
//
// The caller is responsible for managing the sandbox lifecycle.
func (s *Sandbox) CreateHiddenBase(ctx context.Context, snapfilePath string) error {
	ctx, span := tracer.Start(ctx, "create-hidden-base")
	defer span.End()

	if err := s.requestEnvdPrepareBase(ctx); err != nil {
		return fmt.Errorf("failed to prepare hidden base: %w", err)
	}

	if err := s.waitForHiddenBaseReady(ctx); err != nil {
		return fmt.Errorf("hidden base agent not ready: %w", err)
	}

	logger.L().Info(ctx, "hidden base agent ready, pausing VM",
		zap.String("sandbox_id", s.Runtime.SandboxID))

	if err := s.process.Pause(ctx); err != nil {
		return fmt.Errorf("failed to pause VM for hidden base: %w", err)
	}

	if err := s.process.CreateSnapshot(ctx, snapfilePath); err != nil {
		return fmt.Errorf("failed to create hidden base snapshot: %w", err)
	}

	telemetry.ReportEvent(ctx, "hidden base snapshot created")

	return nil
}
