package host

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/cabundle"
)

func TestLazyCleanupSerializesOverlappingUpdates(t *testing.T) {
	t.Parallel()
	tr := newTestTrust(t)
	c := installer()
	a, b, d := newTestCA(t, "A"), newTestCA(t, "B"), newTestCA(t, "D")
	tr.update(t, c, a)
	old := c.requested
	tr.update(t, c, a, b)
	tr.update(t, c, b, d, tr.system[0])
	data, err := os.ReadFile(tr.bundlePath)
	require.NoError(t, err)
	// No background job has run: new roots are usable and retired A still exists.
	require.Contains(t, string(data), a)
	require.Contains(t, string(data), b)
	require.Contains(t, string(data), d)
	require.NoError(t, c.persist(t.Context(), old, tr.bundlePath, tr.certDir))
	after, err := os.ReadFile(tr.bundlePath)
	require.NoError(t, err)
	require.Equal(t, data, after, "superseded task must not remove newer roots")
	require.NoError(t, c.persist(t.Context(), c.requested, tr.bundlePath, tr.certDir))
	tr.requireTrust(t, b, d, tr.system[0])
	tr.install(t, c, d)
	tr.requireTrust(t, d) // A/B retire, preexisting system root stays.
}

func TestLazyCleanupKeepsRootAddedExternallyBetweenAppends(t *testing.T) {
	t.Parallel()
	tr := newTestTrust(t)
	c := installer()
	a, b, u := newTestCA(t, "A"), newTestCA(t, "B"), newTestCA(t, "external")
	tr.update(t, c, a)
	f, err := os.OpenFile(tr.bundlePath, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(u)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	tr.system = append(tr.system, u)
	tr.update(t, c, b, u)
	require.NoError(t, c.persist(t.Context(), c.requested, tr.bundlePath, tr.certDir))
	tr.install(t, c, b)
	tr.requireTrust(t, b)
}

func TestLazyCleanupFailureCanRetryWithoutLosingOwnership(t *testing.T) {
	t.Parallel()
	tr := newTestTrust(t)
	c := installer()
	a, b := newTestCA(t, "A"), newTestCA(t, "B")
	tr.update(t, c, a)
	tr.update(t, c, b)
	require.NoError(t, os.Rename(tr.bundlePath, tr.bundlePath+".saved"))
	require.Error(t, c.persist(t.Context(), c.requested, tr.bundlePath, tr.certDir))
	require.NoError(t, os.Rename(tr.bundlePath+".saved", tr.bundlePath))
	require.True(t, tr.update(t, c, b), "same desired set must reschedule failed cleanup")
	require.NoError(t, c.persist(t.Context(), c.requested, tr.bundlePath, tr.certDir))
	tr.requireTrust(t, b)
}

func TestLazyCleanupDeduplicatesPreexistingRootBeforeRetirement(t *testing.T) {
	t.Parallel()
	tr := newTestTrust(t)
	c := installer()
	a, b := newTestCA(t, "A"), newTestCA(t, "B")
	tr.update(t, c, tr.system[0], a)
	tr.update(t, c, b)
	require.NoError(t, c.persist(t.Context(), c.requested, tr.bundlePath, tr.certDir))
	tr.requireTrust(t, b)
}

func TestLazyCleanupFailureBlocksNewAppend(t *testing.T) {
	t.Parallel()
	tr := newTestTrust(t)
	c := installer()
	a, b := newTestCA(t, "A"), newTestCA(t, "B")
	tr.update(t, c, a)
	previous := c.requested
	require.NoError(t, os.Rename(tr.bundlePath, tr.bundlePath+".saved"))
	parsed, err := cabundle.Parse(b)
	require.NoError(t, err)
	changed, err := c.updateBundle(t.Context(), parsed, tr.bundlePath)
	require.Error(t, err)
	require.False(t, changed)
	require.True(t, c.requested.Equal(previous))
	require.NoError(t, os.Rename(tr.bundlePath+".saved", tr.bundlePath))
	tr.install(t, c, b)
	tr.requireTrust(t, b)
}

func TestOwnershipScanMatchesReconciliation(t *testing.T) {
	t.Parallel()
	a, b := newTestCA(t, "A"), newTestCA(t, "B")
	certs, err := cabundle.Parse(a + b)
	require.NoError(t, err)
	for _, content := range []string{"", a, a + a, reformat(a), a + b, "ignored prefix\n" + a, "-----BEGIN BROKEN-----\ninvalid\n" + a, strings.ReplaceAll(a, "CERTIFICATE", "OTHER")} {
		_, expected := cabundle.Reconcile([]byte(content), nil, certs)
		require.Equal(t, expected, cabundle.Missing([]byte(content), certs))
	}
}
