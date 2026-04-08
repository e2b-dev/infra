package host

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	CaBundlePath = "/etc/ssl/certs/ca-certificates.crt"
	CaStatePath  = "/var/run/e2b/ca-cert.pem"
)

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
// pause/resume cycles. The critical path only appends to the bundle (~2 ms);
// removing the previous cert happens in a background goroutine.
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

	if c.lastCACert == normalized {
		c.logger.Debug().
			Dur("duration", time.Since(start)).
			Msg("CA cert unchanged, skipping install")

		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Snapshot the previous cert before overwriting; used as fallback when no
	// state file exists yet.
	prevPEM := c.lastCACert

	if err := appendToFile(bundlePath, normalized); err != nil {
		c.logger.Error().Err(err).Msg("Failed to append CA cert to bundle")

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
}

// appendToFile opens path in append mode and writes data without truncating.
func appendToFile(path, data string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(data)

	return err
}

// removeCertFromBundle rewrites bundlePath removing all occurrences of certPEM.
// Must be called under mu.
func removeCertFromBundle(bundlePath, certPEM string) error {
	content, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}

	cleaned := strings.ReplaceAll(string(content), certPEM, "")

	return os.WriteFile(bundlePath, []byte(cleaned), 0o644)
}
