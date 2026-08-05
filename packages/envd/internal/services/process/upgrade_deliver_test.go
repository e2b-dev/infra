package process

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteUpgradeBinary_RejectsMissingHeader(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "envd.next")
	err := WriteUpgradeBinary(dest, "", bytes.NewReader([]byte("payload")))
	require.ErrorIs(t, err, ErrUpgradeSHA256Missing)
	_, statErr := os.Stat(dest)
	assert.True(t, os.IsNotExist(statErr), "must not write on missing digest")
}

func TestWriteUpgradeBinary_RejectsMalformedHeader(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  string
	}{
		{"empty-ish garbage", "not-hex"},
		{"too short", "deadbeef"},
		{"uppercase", "DEADBEEFDEADBEEFDEADBEEFDEADBEEFDEADBEEFDEADBEEFDEADBEEFDEADBEEF"},
		{"mixed case", "DeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dest := filepath.Join(t.TempDir(), "envd.next")
			err := WriteUpgradeBinary(dest, tc.hdr, bytes.NewReader([]byte("payload")))
			require.ErrorIs(t, err, ErrUpgradeSHA256Malformed)
			_, statErr := os.Stat(dest)
			assert.True(t, os.IsNotExist(statErr), "must not write on malformed digest")
		})
	}
}

func TestWriteUpgradeBinary_RejectsWrongHash(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "envd.next")
	wrong := sha256.Sum256([]byte("other"))
	err := WriteUpgradeBinary(dest, hex.EncodeToString(wrong[:]), bytes.NewReader([]byte("payload")))
	require.ErrorIs(t, err, ErrUpgradeSHA256Mismatch)
	_, statErr := os.Stat(dest)
	assert.True(t, os.IsNotExist(statErr), "must not write on digest mismatch")
}

func TestWriteUpgradeBinary_AcceptsCorrectHash(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "envd.next")
	payload := []byte("envd-binary-bytes")
	sum := sha256.Sum256(payload)
	err := WriteUpgradeBinary(dest, hex.EncodeToString(sum[:]), bytes.NewReader(payload))
	require.NoError(t, err)

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, payload, got)

	fi, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
}

func TestWriteUpgradeBinary_ReadErrorSurfaced(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "envd.next")
	sum := sha256.Sum256(nil)
	err := WriteUpgradeBinary(dest, hex.EncodeToString(sum[:]), errReader{})
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrUpgradeSHA256Missing))
	assert.False(t, errors.Is(err, ErrUpgradeSHA256Malformed))
	assert.False(t, errors.Is(err, ErrUpgradeSHA256Mismatch))
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
