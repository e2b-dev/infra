package localupload

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// newTestHandler creates a Handler rooted in a temp directory with a known HMAC key.
func newTestHandler(t *testing.T) (*Handler, string, []byte) {
	t.Helper()

	basePath := t.TempDir()
	hmacKey := []byte("test-hmac-secret-key-0123456789ab")
	h := NewHandler(basePath, hmacKey)

	return h, basePath, hmacKey
}

// signedURL builds a request URL with a valid HMAC token for the given path and TTL.
func signedURL(t *testing.T, hmacKey []byte, path string, ttl time.Duration) string {
	t.Helper()

	expires := time.Now().Add(ttl).Unix()
	token := storage.ComputeUploadHMAC(hmacKey, path, expires)

	v := url.Values{}
	v.Set("path", path)
	v.Set("expires", strconv.FormatInt(expires, 10))
	v.Set("token", token)

	return "/upload?" + v.Encode()
}

func TestHandler_PUT_Success(t *testing.T) {
	t.Parallel()

	h, basePath, hmacKey := newTestHandler(t)

	body := "hello upload"
	reqURL := signedURL(t, hmacKey, "dir/file.txt", 5*time.Minute)
	req := httptest.NewRequest(http.MethodPut, reqURL, strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	data, err := os.ReadFile(filepath.Join(basePath, "dir", "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, body, string(data))
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	h, _, hmacKey := newTestHandler(t)

	methods := []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			reqURL := signedURL(t, hmacKey, "file.txt", 5*time.Minute)
			req := httptest.NewRequest(method, reqURL, nil)
			rr := httptest.NewRecorder()

			h.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
		})
	}
}

func TestHandler_MissingQueryParams(t *testing.T) {
	t.Parallel()

	h, _, _ := newTestHandler(t)

	tests := []struct {
		name   string
		params url.Values
	}{
		{
			name:   "missing path",
			params: url.Values{"expires": {"123"}, "token": {"abc"}},
		},
		{
			name:   "missing expires",
			params: url.Values{"path": {"file.txt"}, "token": {"abc"}},
		},
		{
			name:   "missing token",
			params: url.Values{"path": {"file.txt"}, "expires": {"123"}},
		},
		{
			name:   "all missing",
			params: url.Values{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPut, "/upload?"+tt.params.Encode(), nil)
			rr := httptest.NewRecorder()

			h.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusBadRequest, rr.Code)
		})
	}
}

func TestHandler_InvalidExpires(t *testing.T) {
	t.Parallel()

	h, _, _ := newTestHandler(t)

	v := url.Values{}
	v.Set("path", "file.txt")
	v.Set("expires", "not-a-number")
	v.Set("token", "abc")

	req := httptest.NewRequest(http.MethodPut, "/upload?"+v.Encode(), nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandler_InvalidToken(t *testing.T) {
	t.Parallel()

	h, _, _ := newTestHandler(t)

	expires := time.Now().Add(5 * time.Minute).Unix()

	v := url.Values{}
	v.Set("path", "file.txt")
	v.Set("expires", strconv.FormatInt(expires, 10))
	v.Set("token", "definitely-not-valid")

	req := httptest.NewRequest(http.MethodPut, "/upload?"+v.Encode(), nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestHandler_ExpiredToken(t *testing.T) {
	t.Parallel()

	h, _, hmacKey := newTestHandler(t)

	// Token that expired 1 minute ago.
	reqURL := signedURL(t, hmacKey, "file.txt", -1*time.Minute)
	req := httptest.NewRequest(http.MethodPut, reqURL, strings.NewReader("data"))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestHandler_PathTraversal(t *testing.T) {
	t.Parallel()

	h, _, hmacKey := newTestHandler(t)

	maliciousPaths := []string{
		"../etc/passwd",
		"foo/../../etc/shadow",
		"../../../tmp/escape",
	}

	for _, p := range maliciousPaths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()

			reqURL := signedURL(t, hmacKey, p, 5*time.Minute)
			req := httptest.NewRequest(http.MethodPut, reqURL, strings.NewReader("evil"))
			rr := httptest.NewRecorder()

			h.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusBadRequest, rr.Code)
		})
	}
}

func TestHandler_OverwritesExistingFile(t *testing.T) {
	t.Parallel()

	h, basePath, hmacKey := newTestHandler(t)

	// Write initial content.
	reqURL := signedURL(t, hmacKey, "overwrite.txt", 5*time.Minute)
	req := httptest.NewRequest(http.MethodPut, reqURL, strings.NewReader("version1"))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Overwrite with new content.
	reqURL = signedURL(t, hmacKey, "overwrite.txt", 5*time.Minute)
	req = httptest.NewRequest(http.MethodPut, reqURL, strings.NewReader("version2"))
	rr = httptest.NewRecorder()

	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	data, err := os.ReadFile(filepath.Join(basePath, "overwrite.txt"))
	require.NoError(t, err)
	assert.Equal(t, "version2", string(data))
}

func TestHandler_NestedDirectoryCreation(t *testing.T) {
	t.Parallel()

	h, basePath, hmacKey := newTestHandler(t)

	reqURL := signedURL(t, hmacKey, "a/b/c/d/deep.txt", 5*time.Minute)
	req := httptest.NewRequest(http.MethodPut, reqURL, strings.NewReader("deep"))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	data, err := os.ReadFile(filepath.Join(basePath, "a", "b", "c", "d", "deep.txt"))
	require.NoError(t, err)
	assert.Equal(t, "deep", string(data))
}

func TestHandler_EmptyBody(t *testing.T) {
	t.Parallel()

	h, basePath, hmacKey := newTestHandler(t)

	reqURL := signedURL(t, hmacKey, "empty.txt", 5*time.Minute)
	req := httptest.NewRequest(http.MethodPut, reqURL, strings.NewReader(""))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	data, err := os.ReadFile(filepath.Join(basePath, "empty.txt"))
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestHandler_TokenForDifferentPath(t *testing.T) {
	t.Parallel()

	h, _, hmacKey := newTestHandler(t)

	// Sign for "legit.txt" but try to upload to "other.txt"
	expires := time.Now().Add(5 * time.Minute).Unix()
	token := storage.ComputeUploadHMAC(hmacKey, "legit.txt", expires)

	v := url.Values{}
	v.Set("path", "other.txt")
	v.Set("expires", strconv.FormatInt(expires, 10))
	v.Set("token", token)

	req := httptest.NewRequest(http.MethodPut, "/upload?"+v.Encode(), strings.NewReader("data"))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}
