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
func testPaths(t *testing.T) (bundlePath, statePath string) {
	t.Helper()

	dir := t.TempDir()
	bundlePath = filepath.Join(dir, "ca-certificates.crt")
	statePath = filepath.Join(dir, "ca-cert.pem")

	require.NoError(t, os.WriteFile(bundlePath, []byte(baseBundle), 0o644))

	return bundlePath, statePath
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
	bundlePath, statePath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), certA, bundlePath, statePath)

	// State file is written by the background goroutine — wait for it.
	waitForFile(t, statePath)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	assert.Contains(t, string(bundle), strings.TrimRight(certA, "\n"))

	state, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Equal(t, strings.TrimRight(certA, "\n")+"\n", string(state))
}

func TestInstallCACert_SameCert(t *testing.T) {
	t.Parallel()
	bundlePath, statePath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), certA, bundlePath, statePath)
	c.install(context.Background(), certA, bundlePath, statePath) // resume — hot path hit

	waitForFile(t, statePath)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	normalized := strings.TrimRight(certA, "\n") + "\n"
	assert.Equal(t, 1, strings.Count(string(bundle), normalized), "cert should appear exactly once")
}

func TestInstallCACert_DifferentCert(t *testing.T) {
	t.Parallel()
	bundlePath, statePath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), certA, bundlePath, statePath)
	waitForFile(t, statePath) // ensure state file is written before second install

	c.install(context.Background(), certB, bundlePath, statePath)

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
	bundlePath, statePath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), "", bundlePath, statePath)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	assert.Equal(t, baseBundle, string(bundle), "bundle should be untouched")

	_, err = os.Stat(statePath)
	assert.True(t, os.IsNotExist(err), "state file should not be created for empty cert")
}

func TestInstallCACert_RestartSameCert(t *testing.T) {
	t.Parallel()
	// Simulate envd restarting under OOM and receiving the same cert again.
	// Pre-populate both the bundle and the state file as a real previous run
	// would leave them — the goroutine should skip removal since the cert is
	// unchanged.
	bundlePath, statePath := testPaths(t)
	c := newTestInstaller(t)

	normalizedA := strings.TrimRight(certA, "\n") + "\n"

	// State of the VM after a previous envd run.
	require.NoError(t, os.WriteFile(bundlePath, []byte(baseBundle+normalizedA), 0o644))
	require.NoError(t, os.WriteFile(statePath, []byte(normalizedA), 0o644))
	// lastCACert is empty — the process was restarted.

	c.install(context.Background(), certA, bundlePath, statePath)
	waitForFile(t, statePath)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, strings.Count(string(bundle), normalizedA), 1)
}

func TestInstallCACert_RestartDifferentCert(t *testing.T) {
	t.Parallel()
	// Simulate envd restarting and receiving a new cert. The state file must
	// be used to identify and remove the old cert even though lastCACert is
	// empty.
	bundlePath, statePath := testPaths(t)
	c := newTestInstaller(t)

	normalizedA := strings.TrimRight(certA, "\n") + "\n"
	normalizedB := strings.TrimRight(certB, "\n") + "\n"

	// State of the VM after a previous envd run that installed certA.
	require.NoError(t, os.WriteFile(bundlePath, []byte(baseBundle+normalizedA), 0o644))
	require.NoError(t, os.WriteFile(statePath, []byte(normalizedA), 0o644))
	// lastCACert is empty — the process was restarted.

	c.install(context.Background(), certB, bundlePath, statePath)

	waitForBundleChange(t, bundlePath, normalizedA, normalizedB)

	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	assert.NotContains(t, string(bundle), normalizedA, "old cert should be removed using state file")
	assert.Contains(t, string(bundle), normalizedB, "new cert should be present")
}

func TestInstallCACert_ConcurrentResume(t *testing.T) {
	t.Parallel()
	bundlePath, statePath := testPaths(t)
	c := newTestInstaller(t)

	c.install(context.Background(), certA, bundlePath, statePath)
	waitForFile(t, statePath)

	var wg sync.WaitGroup

	for range 10 {
		wg.Go(func() {
			c.install(context.Background(), certA, bundlePath, statePath)
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
	statePath := filepath.Join(dir, "ca-cert.pem")

	normalizedA := strings.TrimRight(certA, "\n") + "\n"
	normalizedB := strings.TrimRight(certB, "\n") + "\n"

	require.NoError(t, os.WriteFile(bundlePath, []byte(baseBundle+normalizedA+normalizedB), 0o644))
	require.NoError(t, removeCertFromBundle(bundlePath, statePath, normalizedA))

	result, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	assert.NotContains(t, string(result), normalizedA)
	assert.Contains(t, string(result), normalizedB)
	assert.Contains(t, string(result), baseBundle)
}
