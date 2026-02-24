package localupload

import (
	"fmt"
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
		http.Error(w, fmt.Sprintf("failed to create directory: %v", err), http.StatusInternalServerError)

		return
	}

	// Write the request body to the file
	f, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create file: %v", err), http.StatusInternalServerError)

		return
	}
	defer f.Close()

	if _, err := io.Copy(f, r.Body); err != nil {
		http.Error(w, fmt.Sprintf("failed to write file: %v", err), http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
}
