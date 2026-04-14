package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	certA = `-----BEGIN CERTIFICATE-----
MIIBcTCCARegAwIBAgIUTestCertA==
-----END CERTIFICATE-----
`
	certB = `-----BEGIN CERTIFICATE-----
MIIBcTCCARegAwIBAgIUTestCertB==
-----END CERTIFICATE-----
`
	baseBundle = "# System CA bundle\n"
)

func newTestInstaller(t *testing.T) *CACertInstaller {
	t.Helper()

	logger := zerolog.Nop()

	return NewCACertInstaller(&logger)
}

// testPaths returns bundle and state paths inside a fresh temp dir, with the
// bundle pre-populated with baseBundle.
func testPaths(t *testing.T) (bundlePath, extraPath string) {
	t.Helper()

	dir := t.TempDir()
	bundlePath = filepath.Join(dir, "ca-certificates.crt")
	extraPath = filepath.Join(dir, "extra", "e2b-ca.crt")

	require.NoError(t, os.WriteFile(bundlePath, []byte(baseBundle), 0o644))

	return bundlePath, extraPath
}

// waitForFile polls until path exists or the deadline is exceeded.
func waitForFile(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s to appear", path)
}

// waitForBundleChange polls until the bundle no longer contains removedPEM
// and does contain keptPEM. Using only the absence check is racy against the
// atomic rename in removeCertFromBundle: the file briefly disappears between
// unlink and rename, satisfying the absence condition prematurely.
func waitForBundleChange(t *testing.T, bundlePath, removedPEM, keptPEM string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		bundle, err := os.ReadFile(bundlePath)
		require.NoError(t, err)

		content := string(bundle)
		if !strings.Contains(content, removedPEM) && strings.Contains(content, keptPEM) {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for cert rotation in bundle")
}

func TestInstallCACert_FirstTime(t *testing.T) {
	t.Parallel()
	bundlePath, extraPath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), certA, bundlePath, extraPath)
	waitForFile(t, extraPath)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	assert.Contains(t, string(bundle), strings.TrimRight(certA, "\n"))

	extra, err := os.ReadFile(extraPath)
	require.NoError(t, err)
	assert.Equal(t, strings.TrimRight(certA, "\n")+"\n", string(extra))
}

func TestInstallCACert_SameCert(t *testing.T) {
	t.Parallel()
	bundlePath, extraPath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), certA, bundlePath, extraPath)
	c.install(context.Background(), certA, bundlePath, extraPath) // resume — hot path hit

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	normalized := strings.TrimRight(certA, "\n") + "\n"
	assert.Equal(t, 1, strings.Count(string(bundle), normalized), "cert should appear exactly once")
}

func TestInstallCACert_DifferentCert(t *testing.T) {
	t.Parallel()
	bundlePath, extraPath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), certA, bundlePath, extraPath)

	c.install(context.Background(), certB, bundlePath, extraPath)

	normalizedA := strings.TrimRight(certA, "\n") + "\n"
	normalizedB := strings.TrimRight(certB, "\n") + "\n"

	waitForBundleChange(t, bundlePath, normalizedA, normalizedB)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	assert.NotContains(t, string(bundle), normalizedA, "old cert should be removed")
	assert.Contains(t, string(bundle), normalizedB, "new cert should be present")
}

func TestInstallCACert_EmptyCert(t *testing.T) {
	t.Parallel()
	bundlePath, extraPath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), "", bundlePath, extraPath)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	assert.Equal(t, baseBundle, string(bundle), "bundle should be untouched")

	_, err = os.Stat(extraPath)
	assert.True(t, os.IsNotExist(err), "extra cert file should not be created for empty cert")
}

func TestInstallCACert_RestartSameCert(t *testing.T) {
	t.Parallel()
	// Simulate envd restarting under OOM and receiving the same cert again.
	// lastCACert is empty after restart so the cert is appended again; the
	// goroutine skips removal (prevPEM == "") leaving a temporary duplicate.
	bundlePath, extraPath := testPaths(t)
	c := newTestInstaller(t)

	normalizedA := strings.TrimRight(certA, "\n") + "\n"

	// State of the VM after a previous envd run.
	require.NoError(t, os.WriteFile(bundlePath, []byte(baseBundle+normalizedA), 0o644))

	c.install(context.Background(), certA, bundlePath, extraPath)
	waitForFile(t, extraPath)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, strings.Count(string(bundle), normalizedA), 1)
}

func TestInstallCACert_RestartDifferentCert(t *testing.T) {
	t.Parallel()
	// Simulate envd restarting and receiving a new cert. Without a state file,
	// prevPEM is "" so the old cert is NOT removed from the bundle immediately —
	// it stays until update-ca-certificates rebuilds from extraPath (which holds
	// only the new cert).
	bundlePath, extraPath := testPaths(t)
	c := newTestInstaller(t)

	normalizedA := strings.TrimRight(certA, "\n") + "\n"
	normalizedB := strings.TrimRight(certB, "\n") + "\n"

	// State of the VM after a previous envd run that installed certA.
	require.NoError(t, os.WriteFile(bundlePath, []byte(baseBundle+normalizedA), 0o644))

	c.install(context.Background(), certB, bundlePath, extraPath)
	waitForFile(t, extraPath)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	assert.Contains(t, string(bundle), normalizedB, "new cert should be appended")

	extra, err := os.ReadFile(extraPath)
	require.NoError(t, err)
	assert.Equal(t, normalizedB, string(extra), "extra dir should hold only the new cert")
}

func TestInstallCACert_ConcurrentResume(t *testing.T) {
	t.Parallel()
	bundlePath, extraPath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), certA, bundlePath, extraPath)

	var wg sync.WaitGroup

	for range 10 {
		wg.Go(func() {
			c.install(context.Background(), certA, bundlePath, extraPath)
		})
	}

	wg.Wait()

	// Acquire mu to drain any in-flight background goroutines.
	c.mu.Lock()
	c.mu.Unlock() //nolint:staticcheck

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	normalized := strings.TrimRight(certA, "\n") + "\n"
	assert.Equal(t, 1, strings.Count(string(bundle), normalized), "cert should appear exactly once")
}

func TestRemoveCertFromBundle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "bundle.crt")

	normalizedA := strings.TrimRight(certA, "\n") + "\n"
	normalizedB := strings.TrimRight(certB, "\n") + "\n"

	require.NoError(t, os.WriteFile(bundlePath, []byte(baseBundle+normalizedA+normalizedB), 0o644))
	require.NoError(t, removeCertFromBundle(bundlePath, normalizedA))

	result, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	assert.NotContains(t, string(result), normalizedA)
	assert.Contains(t, string(result), normalizedB)
	assert.Contains(t, string(result), baseBundle)
}
