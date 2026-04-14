package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	CaBundlePath = "/etc/ssl/certs/ca-certificates.crt"
	CaStatePath  = E2BRunDir + "/ca-cert.pem"
)

// CACertInstaller manages installation of a CA certificate into the VM's
// system trust bundle.
//
// /etc/ssl/certs/ is bind-mounted from tmpfs at VM boot (see envd.service
// ExecStartPre), so all reads and writes bypass the NBD-backed filesystem and
// atomic cert rotation via os.Rename works within the same device.
type CACertInstaller struct {
	mu     sync.Mutex
	logger *zerolog.Logger

	// lastCACert caches the most recently installed PEM so that resume (same
	// cert, same process) is a zero-I/O hot-path hit. Empty on process start;
	// the state file at CaStatePath is the durable record across restarts.
	lastCACert string
}

func NewCACertInstaller(logger *zerolog.Logger) *CACertInstaller {
	return &CACertInstaller{logger: logger}
}

// Install injects certPEM into the system CA bundle. Returns an error if the
// foreground append fails so the caller can signal init failure to the
// orchestrator.
func (c *CACertInstaller) Install(ctx context.Context, certPEM string) error {
	return c.install(ctx, certPEM, CaBundlePath, CaStatePath)
}

// install is the testable core; tests supply their own paths.
//
// The cert changes on every sandbox create but stays the same across
// pause/resume cycles. The critical path only appends to the bundle (fast on
// tmpfs); removing the previous cert happens in a background goroutine.
//
// The state file survives process restarts (OOM, crashes). The background
// goroutine reads it to find the previously installed cert — lastCACert is ""
// after a restart and cannot be used for that purpose.
//
// All goroutine work runs under mu to keep the bundle and state file
// consistent with concurrent foreground appends.
func (c *CACertInstaller) install(_ context.Context, certPEM, bundlePath, statePath string) error {
	if certPEM == "" {
		return nil
	}

	start := time.Now()

	// Normalise to a single trailing newline so comparisons and removals are
	// consistent regardless of how the caller formatted the PEM.
	normalized := strings.TrimRight(certPEM, "\n") + "\n"

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.lastCACert == normalized {
		c.logger.Debug().
			Dur("duration", time.Since(start)).
			Msg("CA cert unchanged, skipping install")

		return nil
	}

	// Snapshot the previous cert before overwriting; used as fallback when no
	// state file exists yet.
	prevPEM := c.lastCACert

	f, err := os.OpenFile(bundlePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open CA bundle: %w", err)
	}

	_, err = f.WriteString(normalized)
	f.Close()

	if err != nil {
		return fmt.Errorf("append CA cert: %w", err)
	}

	c.lastCACert = normalized

	c.logger.Info().
		Dur("append_duration", time.Since(start)).
		Msg("CA cert appended to bundle")

	go func() {
		cleanStart := time.Now()

		c.mu.Lock()
		defer c.mu.Unlock()

		// A newer install has taken over; let that goroutine handle cleanup.
		if c.lastCACert != normalized {
			return
		}

		// State file takes priority over the in-memory prevPEM: it holds the
		// cert from the previous process lifetime after a restart.
		stateRaw, _ := os.ReadFile(statePath)
		effectivePrev := string(stateRaw)
		if effectivePrev == "" {
			effectivePrev = prevPEM
		}

		if err := os.WriteFile(statePath, []byte(normalized), 0o644); err != nil {
			c.logger.Error().Err(err).Msg("Failed to write CA cert state file")

			return
		}

		// No prior cert, or same cert received again after a restart.
		if effectivePrev == "" || effectivePrev == normalized {
			return
		}

		if err := removeCertFromBundle(bundlePath, effectivePrev); err != nil {
			c.logger.Error().Err(err).Msg("Failed to remove old CA cert from bundle")

			return
		}

		c.logger.Info().
			Dur("cleanup_duration", time.Since(cleanStart)).
			Msg("Old CA cert removed from bundle")
	}()

	return nil
}

// removeCertFromBundle rewrites bundlePath removing all occurrences of certPEM.
// The write is atomic (write to temp file in the same directory, then rename)
// so the bundle is never partially written from the perspective of concurrent
// readers. Since /etc/ssl/certs/ is bind-mounted from tmpfs, the temp file and
// the target are on the same device — the rename never crosses filesystem
// boundaries (no EXDEV).
//
// Must be called under mu.
func removeCertFromBundle(bundlePath, certPEM string) error {
	tmpDir := filepath.Dir(bundlePath)

	content, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}

	cleaned := strings.ReplaceAll(string(content), certPEM, "")

	tmp, err := os.CreateTemp(tmpDir, "ca-bundle-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	tmpPath := tmp.Name()

	// os.CreateTemp creates with 0600; restore world-readable so non-root
	// processes can still verify TLS after the rename replaces the bundle.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(tmpPath)

		return fmt.Errorf("chmod temp file: %w", err)
	}

	if _, err := tmp.WriteString(cleaned); err != nil {
		tmp.Close()
		os.Remove(tmpPath)

		return fmt.Errorf("write temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)

		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, bundlePath); err != nil {
		os.Remove(tmpPath)

		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}
