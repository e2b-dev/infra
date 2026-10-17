//go:build linux

package metrics

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeSysctls(values map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		v, ok := values[path]
		if !ok {
			return nil, os.ErrNotExist
		}

		return []byte(v), nil
	}
}

func TestReadHugePagesCapacityAddsTheOvercommitAllowance(t *testing.T) {
	t.Parallel()

	capacity, err := readHugePagesCapacity(fakeSysctls(map[string]string{
		nrHugePagesPath:           "1600\n",
		nrOvercommitHugePagesPath: "400\n",
	}))

	require.NoError(t, err)
	assert.Equal(t, uint64(2000), capacity)
}

func TestReadHugePagesCapacityCountsAPoolThatIsAllOvercommit(t *testing.T) {
	t.Parallel()

	capacity, err := readHugePagesCapacity(fakeSysctls(map[string]string{
		nrHugePagesPath:           "0\n",
		nrOvercommitHugePagesPath: "54097\n",
	}))

	require.NoError(t, err)
	assert.Equal(t, uint64(54097), capacity)
}

// A missing sysctl means the kernel has no hugetlb; any other failure is
// returned so the caller can warn that the node is left unlimited.
func TestReadHugePagesCapacityIsUnknownWhenASysctlCannotBeRead(t *testing.T) {
	t.Parallel()

	missing, missingErr := readHugePagesCapacity(fakeSysctls(map[string]string{nrHugePagesPath: "1600"}))
	garbage, garbageErr := readHugePagesCapacity(fakeSysctls(map[string]string{nrHugePagesPath: "1600", nrOvercommitHugePagesPath: "lots"}))
	failing, failingErr := readHugePagesCapacity(func(string) ([]byte, error) { return nil, errors.New("permission denied") })

	assert.Zero(t, missing)
	require.ErrorIs(t, missingErr, fs.ErrNotExist)
	assert.Zero(t, garbage)
	require.Error(t, garbageErr)
	require.NotErrorIs(t, garbageErr, fs.ErrNotExist)
	assert.Zero(t, failing)
	require.Error(t, failingErr)
	require.NotErrorIs(t, failingErr, fs.ErrNotExist)
}

// Placement gates on these counters, so they must reflect the host when asked,
// not the last poll: no poll has run here.
func TestGetMemoryMetricsReadsTheHostOnRequest(t *testing.T) {
	t.Parallel()

	memory, err := NewHostMetrics().GetMemoryMetrics(t.Context())
	require.NoError(t, err)

	assert.NotZero(t, memory.TotalBytes)
}

func TestMemoryReadReplacesTheRememberedSample(t *testing.T) {
	t.Parallel()

	h := NewHostMetrics()
	older := &MemoryMetrics{TotalBytes: 1 << 30, HugePagesCapacity: 1600}
	fresh := &MemoryMetrics{TotalBytes: 1 << 30, HugePagesCapacity: 2000}

	_, err := h.keepLastGood(older, nil)
	require.NoError(t, err)

	memory, err := h.keepLastGood(fresh, nil)
	require.NoError(t, err)
	assert.Equal(t, fresh, memory)

	memory, err = h.keepLastGood(nil, errors.New("read /proc/meminfo: input/output error"))
	require.NoError(t, err)
	assert.Equal(t, fresh, memory)
}

func TestMemoryReadFailureServesTheLastGoodSample(t *testing.T) {
	t.Parallel()

	h := NewHostMetrics()
	good := &MemoryMetrics{TotalBytes: 1 << 30, HugePagesCapacity: 2000}

	_, err := h.keepLastGood(good, nil)
	require.NoError(t, err)

	memory, err := h.keepLastGood(nil, errors.New("read /proc/meminfo: input/output error"))
	require.NoError(t, err)
	assert.Equal(t, good, memory)
}

func TestMemoryReadFailureBeforeAnyGoodReadReturnsTheError(t *testing.T) {
	t.Parallel()

	readErr := errors.New("read /proc/meminfo: input/output error")

	memory, err := NewHostMetrics().keepLastGood(nil, readErr)
	require.ErrorIs(t, err, readErr)
	assert.Nil(t, memory)
}
