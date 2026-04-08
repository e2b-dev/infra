package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
)

const (
	CaBundlePath = "/etc/ssl/certs/ca-certificates.crt"
	CaStatePath  = E2BRunDir + "/ca-cert.pem"

	// caBundleTmpfsPath is the tmpfs-backed copy of the CA bundle.
	// CaBundlePath is bind-mounted over this so all writes bypass NBD.
	caBundleTmpfsPath = E2BRunDir + "/ca-certificates.crt"
)

// BindMountCABundle copies the system CA bundle to tmpfs and bind-mounts it
// back over the original path so all subsequent reads and writes bypass the
// NBD-backed filesystem entirely. This reduces CA cert injection from ~2 ms
// (warm NBD) / ~460 ms (cold GCS) to ~0.01 ms.
//
// Must be called once at startup, before any /init handler runs. No-op if the
// bind mount is already in place (safe to call after a process restart).
func BindMountCABundle() error {
	// Read the full bundle into memory before any write. On process restart the
	// bind mount is already in place, meaning CaBundlePath and caBundleTmpfsPath
	// are the same inode. Opening caBundleTmpfsPath with O_TRUNC while a read fd
	// is open on CaBundlePath would zero the file before io.Copy runs, destroying
	// the bundle. os.ReadFile completes the read atomically before we write.
	content, err := os.ReadFile(CaBundlePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(caBundleTmpfsPath), 0o755); err != nil {
		return err
	}

	if err := os.WriteFile(caBundleTmpfsPath, content, 0o644); err != nil {
		return err
	}

	// Bind-mount the tmpfs file over the original bundle path.
	// MS_BIND makes the target appear as the source; the underlying NBD file
	// is shadowed for all processes in this mount namespace.
	if err := syscall.Mount(caBundleTmpfsPath, CaBundlePath, "", syscall.MS_BIND, ""); err != nil {
		// EBUSY means the bind mount is already in place (process restart).
		if err == syscall.EBUSY {
			return nil
		}

		return err
	}

	return nil
}

// CACertInstaller manages installation of a CA certificate into the VM's
// system trust bundle.
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

// Install injects certPEM into the system CA bundle.
func (c *CACertInstaller) Install(ctx context.Context, certPEM string) {
	c.install(ctx, certPEM, CaBundlePath, CaStatePath)
}

// install is the testable core; tests supply their own paths.
//
// The cert changes on every sandbox create but stays the same across
// pause/resume cycles. The critical path only appends to the bundle (~0.04 ms
// after BindMountCABundle moves the file to tmpfs); removing the previous cert
// happens in a background goroutine.
//
// The state file survives process restarts (OOM, crashes). The background
// goroutine reads it to find the previously installed cert — lastCACert is ""
// after a restart and cannot be used for that purpose.
//
// All goroutine work runs under mu to keep the bundle and state file
// consistent with concurrent foreground appends.
func (c *CACertInstaller) install(_ context.Context, certPEM, bundlePath, statePath string) {
	if certPEM == "" {
		return
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

		return
	}

	// Snapshot the previous cert before overwriting; used as fallback when no
	// state file exists yet.
	prevPEM := c.lastCACert

	f, err := os.OpenFile(bundlePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		c.logger.Error().Err(err).Msg("Failed to open CA bundle")

		return
	}

	_, err = f.WriteString(normalized)
	f.Close()

	if err != nil {
		c.logger.Error().Err(err).Msg("Failed to write CA cert to bundle")

		return
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

		if err := removeCertFromBundle(bundlePath, statePath, effectivePrev); err != nil {
			c.logger.Error().Err(err).Msg("Failed to remove old CA cert from bundle")

			return
		}

		c.logger.Info().
			Dur("cleanup_duration", time.Since(cleanStart)).
			Msg("Old CA cert removed from bundle")
	}()
}

// removeCertFromBundle rewrites bundlePath removing all occurrences of certPEM.
// The write is atomic (write to temp file, then rename) so the bundle is never
// empty from the perspective of concurrent readers.
//
// tmpDir must be on the same filesystem as bundlePath. In production bundlePath
// is a bind-mounted file whose parent directory is on the NBD-backed filesystem;
// a temp file created there would be on a different device and os.Rename would
// fail with EXDEV. Passing filepath.Dir(statePath) (which is E2BRunDir — the
// same tmpfs as the bind mount source) keeps both files on the same device.
//
// Must be called under mu.
func removeCertFromBundle(bundlePath, statePath, certPEM string) error {
	tmpDir := filepath.Dir(statePath)
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
