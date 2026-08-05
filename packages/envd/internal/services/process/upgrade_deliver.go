package process

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// UpgradeSha256Header carries the lowercase hex SHA-256 digest of a non-empty
// POST /upgrade request body. envd refuses to materialize the body on disk
// unless this header is present, well-formed, and matches the bytes — a
// content-integrity check complementary to the authenticated trigger and the
// fixed-path allowlist on DefaultUpgradeBinPath.
const UpgradeSha256Header = "X-Envd-Upgrade-Sha256"

var (
	ErrUpgradeSHA256Missing   = errors.New("missing X-Envd-Upgrade-Sha256 header")
	ErrUpgradeSHA256Malformed = errors.New("malformed X-Envd-Upgrade-Sha256 header")
	ErrUpgradeSHA256Mismatch  = errors.New("upgrade body SHA-256 mismatch")
)

func parseUpgradeSHA256Header(h string) ([]byte, error) {
	if h == "" {
		return nil, ErrUpgradeSHA256Missing
	}
	if len(h) != hex.EncodedLen(sha256.Size) {
		return nil, ErrUpgradeSHA256Malformed
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil, ErrUpgradeSHA256Malformed
		}
	}
	sum, err := hex.DecodeString(h)
	if err != nil {
		return nil, ErrUpgradeSHA256Malformed
	}

	return sum, nil
}

// WriteUpgradeBinary reads body, verifies it against the lowercase-hex SHA-256
// in sha256Header, and only then writes the bytes to destPath (mode 0o755).
// The destination is unlinked first so a chained upgrade (envd already running
// from destPath) does not hit ETXTBSY on O_TRUNC.
func WriteUpgradeBinary(destPath, sha256Header string, body io.Reader) error {
	want, err := parseUpgradeSHA256Header(sha256Header)
	if err != nil {
		return err
	}

	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	got := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(got[:], want) != 1 {
		return ErrUpgradeSHA256Mismatch
	}

	_ = os.Remove(destPath)
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}

	return closeErr
}
