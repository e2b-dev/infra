package localupload

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// Handler serves local file uploads for filesystem-backed storage.
// It validates HMAC-signed tokens before accepting PUT requests.
// This handler is only registered when STORAGE_PROVIDER=Local.
type Handler struct {
	basePath string
	hmacKey  []byte
}

// NewHandler creates a new local upload handler.
// basePath is the root directory where uploaded files are stored.
// hmacKey is the HMAC key used to validate upload tokens.
func NewHandler(basePath string, hmacKey []byte) *Handler {
	return &Handler{
		basePath: basePath,
		hmacKey:  hmacKey,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	path := r.URL.Query().Get("path")
	expiresStr := r.URL.Query().Get("expires")
	token := r.URL.Query().Get("token")

	if path == "" || expiresStr == "" || token == "" {
		http.Error(w, "missing required query parameters: path, expires, token", http.StatusBadRequest)

		return
	}

	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid expires value", http.StatusBadRequest)

		return
	}

	// Validate the HMAC token and check expiry
	if !storage.ValidateUploadToken(h.hmacKey, path, expires, token) {
		http.Error(w, "invalid or expired token", http.StatusForbidden)

		return
	}

	// Prevent path traversal
	if !filepath.IsLocal(path) {
		http.Error(w, "invalid path", http.StatusBadRequest)

		return
	}

	fullPath := filepath.Join(h.basePath, path)

	// Create parent directories
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "failed to create directory", http.StatusInternalServerError)

		return
	}

	// Write to a temp file in the same directory, then atomically rename on
	// success. This avoids leaving partial/corrupt files on disk if the write
	// fails (e.g. client disconnect, disk full).
	tmpFile, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		http.Error(w, "failed to create temporary file", http.StatusInternalServerError)

		return
	}

	stored := false
	defer func() {
		if !stored {
			os.Remove(tmpFile.Name())
		}
	}()

	if _, err := io.Copy(tmpFile, r.Body); err != nil {
		tmpFile.Close()
		http.Error(w, "failed to write file", http.StatusInternalServerError)

		return
	}

	if err := tmpFile.Close(); err != nil {
		http.Error(w, "failed to finalize file", http.StatusInternalServerError)

		return
	}

	if err := os.Chmod(tmpFile.Name(), 0o644); err != nil {
		http.Error(w, "failed to set file permissions", http.StatusInternalServerError)

		return
	}

	if err := os.Rename(tmpFile.Name(), fullPath); err != nil {
		http.Error(w, "failed to store file", http.StatusInternalServerError)

		return
	}

	stored = true

	w.WriteHeader(http.StatusOK)
}
