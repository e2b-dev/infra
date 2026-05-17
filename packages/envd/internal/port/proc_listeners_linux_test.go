//go:build linux

package port

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeProcAddrV4(t *testing.T) {
	t.Parallel()
	ip, port, err := decodeProcAddr("0100007F:1F90", syscall.AF_INET)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", ip)
	assert.Equal(t, uint16(8080), port)
}

func TestDecodeProcAddrV4_Wildcard(t *testing.T) {
	t.Parallel()
	ip, port, err := decodeProcAddr("00000000:0050", syscall.AF_INET)
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0", ip)
	assert.Equal(t, uint16(80), port)
}

func TestDecodeProcAddrV6_Loopback(t *testing.T) {
	t.Parallel()
	// IPv6 loopback ::1 expressed in /proc/net/tcp6 form (4-byte groups in CPU
	// byte order — for little-endian that's the last group as 01000000).
	ip, port, err := decodeProcAddr("00000000000000000000000001000000:1F90", syscall.AF_INET6)
	require.NoError(t, err)
	assert.Equal(t, "::1", ip)
	assert.Equal(t, uint16(8080), port)
}

func TestDecodeProcAddr_InvalidFamily(t *testing.T) {
	t.Parallel()
	_, _, err := decodeProcAddr("00000000:0050", syscall.AF_UNIX)
	assert.Error(t, err)
}

func TestDecodeProcAddr_MalformedPort(t *testing.T) {
	t.Parallel()
	_, _, err := decodeProcAddr("0100007F:ZZZZ", syscall.AF_INET)
	assert.Error(t, err)
}
