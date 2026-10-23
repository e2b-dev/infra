package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/envd/internal/cabundle"
)

var ErrCAInstallInProgress = errors.New("CA install already in progress")

var managedCertFilename = regexp.MustCompile(`^e2b-([0-9a-f]{64})\.crt$`)

const (
	CaBundlePath     = "/etc/ssl/certs/ca-certificates.crt"
	caCertDir        = "/usr/local/share/ca-certificates"
	legacyCACertFile = "e2b-ca.crt"
)

// CACertInstaller replaces the certificates it injected into the tmpfs bundle.
// Ownership is process-local, so a restart can leave old certificates until a
// trust-store rebuild. Rootfs persistence runs in the background.
type CACertInstaller struct {
	mu        *semaphore.Weighted
	logger    *zerolog.Logger
	requested cabundle.Bundle
	owned     cabundle.Bundle
	pending   *pendingCAAppend
}

type pendingCAAppend struct {
	before []byte
}

func NewCACertInstaller(logger *zerolog.Logger) *CACertInstaller {
	return &CACertInstaller{logger: logger, mu: semaphore.NewWeighted(1)}
}

// Install makes the certificates in bundlePEM the set envd trusts on behalf of
// the orchestrator. Requested roots are present before Install returns.
// Retired roots and duplicate certificates are removed in the background,
// followed by persistence for later trust-store rebuilds.
func (c *CACertInstaller) Install(ctx context.Context, bundlePEM string) error {
	return c.install(ctx, bundlePEM, CaBundlePath, caCertDir)
}

// install is the testable core; tests supply their own paths.
func (c *CACertInstaller) install(ctx context.Context, bundlePEM, bundlePath, certDir string) error {
	if bundlePEM == "" {
		return nil
	}

	certs, err := cabundle.Parse(bundlePEM)
	if err != nil {
		return fmt.Errorf("parse CA bundle: %w", err)
	}

	start := time.Now()
	changed, err := c.updateBundle(ctx, certs, bundlePath)
	if err != nil || !changed {
		return err
	}

	c.logger.Info().
		Dur("duration", time.Since(start)).
		Int("certificates", len(certs)).
		Msg("CA certificates installed in bundle")

	go func() {
		persistStart := time.Now()
		if err := c.persist(context.WithoutCancel(ctx), certs, bundlePath, certDir); err != nil {
			c.logger.Error().Err(err).Msg("Failed to persist CA certificates")

			return
		}

		c.logger.Info().
			Dur("duration", time.Since(persistStart)).
			Msg("CA certificates persisted")
	}()

	return nil
}

// updateBundle finishes any pending cleanup, then atomically appends requested roots.
func (c *CACertInstaller) updateBundle(ctx context.Context, certs cabundle.Bundle, bundlePath string) (bool, error) {
	if err := c.mu.Acquire(ctx, 1); err != nil {
		return false, fmt.Errorf("%w: %w", ErrCAInstallInProgress, err)
	}
	defer c.mu.Release(1)
	if c.requested.Equal(certs) {
		return c.pending != nil, nil
	}
	if err := c.cleanupBundle(bundlePath); err != nil {
		return false, err
	}
	content, err := os.ReadFile(bundlePath)
	if err != nil {
		return false, fmt.Errorf("read CA bundle: %w", err)
	}
	before := content
	content = bytes.Clone(content)
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	for _, cert := range certs {
		content = append(content, cert.PEM...)
	}
	if err := writeFileAtomic(bundlePath, content); err != nil {
		return false, fmt.Errorf("write CA bundle: %w", err)
	}
	c.pending = &pendingCAAppend{before: before}
	c.requested = certs

	return true, nil
}

// persist skips superseded installs so delayed work cannot restore old roots.
func (c *CACertInstaller) persist(ctx context.Context, certs cabundle.Bundle, bundlePath, certDir string) error {
	if err := c.mu.Acquire(ctx, 1); err != nil {
		return err
	}
	defer c.mu.Release(1)
	if !c.requested.Equal(certs) {
		return nil
	}
	if err := c.cleanupBundle(bundlePath); err != nil {
		return err
	}
	if err := writeCertFiles(certDir, certs); err != nil {
		return err
	}

	return removeRetiredCertFiles(certDir, certs)
}

// cleanupBundle runs with mu held. Only one append can await reconciliation.
func (c *CACertInstaller) cleanupBundle(bundlePath string) error {
	if c.pending == nil {
		return nil
	}
	// The pre-append snapshot distinguishes our additions from preexisting roots.
	added := cabundle.Missing(c.pending.before, c.requested)
	owned := append(append(cabundle.Bundle(nil), c.owned...), added...)
	content, err := os.ReadFile(bundlePath)
	if err != nil {
		return fmt.Errorf("read CA bundle for cleanup: %w", err)
	}
	content, nextOwned := cabundle.Reconcile(content, owned, c.requested)
	if err := writeFileAtomic(bundlePath, content); err != nil {
		return fmt.Errorf("clean CA bundle: %w", err)
	}
	c.owned, c.pending = nextOwned, nil

	return nil
}

func writeCertFiles(certDir string, certs cabundle.Bundle) error {
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return fmt.Errorf("create CA cert dir: %w", err)
	}
	for _, cert := range certs {
		name := makeCertName(cert.Fingerprint)
		if err := writeFileAtomic(filepath.Join(certDir, name), cert.PEM); err != nil {
			return fmt.Errorf("persist CA cert: %w", err)
		}
	}

	return nil
}

func removeRetiredCertFiles(certDir string, certs cabundle.Bundle) error {
	wanted := make(map[string]struct{}, len(certs))
	for _, cert := range certs {
		wanted[cert.Fingerprint] = struct{}{}
	}
	entries, err := os.ReadDir(certDir)
	if err != nil {
		return fmt.Errorf("read CA cert dir: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == legacyCACertFile {
			keep, err := legacyCertRequested(filepath.Join(certDir, name), wanted)
			if err != nil {
				return err
			}
			if keep {
				continue
			}
		} else {
			match := managedCertFilename.FindStringSubmatch(name)
			if match == nil {
				continue
			}
			if _, keep := wanted[match[1]]; keep {
				continue
			}
		}
		if err := os.Remove(filepath.Join(certDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale CA cert: %w", err)
		}
	}

	return nil
}

// Existing OpenSSL hash links still resolve through the legacy source filename.
func legacyCertRequested(path string, wanted map[string]struct{}) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read legacy CA cert: %w", err)
	}
	legacy, err := cabundle.Parse(string(data))
	if err == nil && len(legacy) == 1 {
		_, keep := wanted[legacy[0].Fingerprint]

		return keep, nil
	}

	return false, nil
}

func makeCertName(fingerprint string) string {
	return fmt.Sprintf("e2b-%s.crt", fingerprint)
}

// writeFileAtomic replaces path with data through a temp file in the same
// directory. The temp name starts with a dot so trust-store tools skip it.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	// os.CreateTemp creates with 0600; non-root processes must read trust.
	err = tmp.Chmod(0o644)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpPath, path)
	}
	if err != nil {
		os.Remove(tmpPath)

		return err
	}

	return nil
}
