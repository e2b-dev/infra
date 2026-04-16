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
	fsMountTimeout   = 10 * time.Second
	fsUnmountTimeout = 10 * time.Second
	fsSyncTimeout    = 10 * time.Second
)

func (s *Sandbox) setEnvdAccessToken(req *http.Request) {
	if s.Config.Envd.AccessToken != nil {
		req.Header.Set("X-Access-Token", *s.Config.Envd.AccessToken)
	}
}

// requestEnvdMountOverlay tells the guest agent to mount the overlay
// upper device (/dev/vdb), set up OverlayFS merging it with the rootfs,
// and pivot_root into the overlay. After this, the user sees a single
// writable filesystem.
func (s *Sandbox) requestEnvdMountOverlay(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "envd-mount-overlay")
	defer span.End()

	address := fmt.Sprintf("http://%s:%d/fs-snapshot/mount-overlay", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	reqCtx, cancel := context.WithTimeout(ctx, fsMountTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, address, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	s.setEnvdAccessToken(req)

	resp, err := sandboxHttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mount-overlay request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mount-overlay returned %d: %s", resp.StatusCode, string(body))
	}

	telemetry.ReportEvent(ctx, "overlay mounted and pivoted")

	return nil
}

// requestEnvdUnmountOverlay tells the guest agent to sync, pivot back
// to the original rootfs, and unmount the overlay + upper device.
// After this, only the rootfs (disk A) is mounted and disk B is safe
// to snapshot from the host side.
func (s *Sandbox) requestEnvdUnmountOverlay(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "envd-unmount-overlay")
	defer span.End()

	address := fmt.Sprintf("http://%s:%d/fs-snapshot/unmount-overlay", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	reqCtx, cancel := context.WithTimeout(ctx, fsUnmountTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, address, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	s.setEnvdAccessToken(req)

	resp, err := sandboxHttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("unmount-overlay request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unmount-overlay returned %d: %s", resp.StatusCode, string(body))
	}

	telemetry.ReportEvent(ctx, "overlay unmounted, disk B safe to snapshot")

	return nil
}

// requestEnvdSync tells the guest agent to flush all pending filesystem
// writes to disk.
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

	s.setEnvdAccessToken(req)

	resp, err := sandboxHttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sync request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sync returned %d: %s", resp.StatusCode, string(body))
	}

	telemetry.ReportEvent(ctx, "filesystem synced")

	return nil
}

// CreateHiddenBase takes a running sandbox (booted from disk A, no overlay
// mounted yet) and creates a snapshot. This snapshot captures kernel memory,
// network state, and systemd+envd running from the rootfs — but with the
// overlay disk B NOT mounted.
//
// On FS-only resume, this snapshot is restored and envd mounts disk B
// (with saved user data) as the overlay upper, giving a fresh ext4 mount
// with no stale metadata.
func (s *Sandbox) CreateHiddenBase(ctx context.Context, snapfilePath string) error {
	ctx, span := tracer.Start(ctx, "create-hidden-base")
	defer span.End()

	logger.L().Info(ctx, "creating hidden base snapshot (no overlay mounted)",
		zap.String("sandbox_id", s.Runtime.SandboxID))

	if err := s.process.Pause(ctx); err != nil {
		return fmt.Errorf("failed to pause VM: %w", err)
	}

	if err := s.process.CreateSnapshot(ctx, snapfilePath); err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}

	telemetry.ReportEvent(ctx, "hidden base snapshot created")

	return nil
}
