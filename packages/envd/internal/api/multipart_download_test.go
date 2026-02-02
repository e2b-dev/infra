package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
)

func setupTestAPIForDownload(t *testing.T) *API {
	t.Helper()
	logger := zerolog.New(io.Discard)
	defaults := &execcontext.Defaults{}
	api := New(&logger, defaults, nil, true)
	t.Cleanup(func() {
		api.Close()
	})
	return api
}

func TestPostFilesDownloadInit(t *testing.T) {
	t.Parallel()

	t.Run("init session returns correct metadata", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		testContent := []byte("Hello, World! This is test content for download.")
		err := os.WriteFile(testFile, testContent, 0o644)
		require.NoError(t, err)

		// Create request body
		body := PostFilesDownloadInitJSONBody{
			Path: testFile,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})

		resp := w.Result()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var result MultipartDownloadInit
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.NotEmpty(t, result.DownloadId)
		assert.Equal(t, int64(len(testContent)), result.TotalSize)
		assert.Equal(t, int64(defaultDownloadPartSize), result.PartSize)
		assert.Equal(t, 1, result.NumParts) // Small file fits in one part
	})

	t.Run("init session with custom part size", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file larger than default part size
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "largefile.txt")
		testContent := make([]byte, 1024*1024) // 1MB
		for i := range testContent {
			testContent[i] = byte(i % 256)
		}
		err := os.WriteFile(testFile, testContent, 0o644)
		require.NoError(t, err)

		customPartSize := int64(256 * 1024) // 256KB
		body := PostFilesDownloadInitJSONBody{
			Path:     testFile,
			PartSize: &customPartSize,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})

		resp := w.Result()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var result MultipartDownloadInit
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.Equal(t, customPartSize, result.PartSize)
		assert.Equal(t, 4, result.NumParts) // 1MB / 256KB = 4 parts
	})

	t.Run("file not found returns 404", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		body := PostFilesDownloadInitJSONBody{
			Path: "/nonexistent/file.txt",
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})

		resp := w.Result()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("directory path returns 400", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		tempDir := t.TempDir()

		body := PostFilesDownloadInitJSONBody{
			Path: tempDir,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})

		resp := w.Result()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("empty file returns 0 parts", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create an empty test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "empty.txt")
		err := os.WriteFile(testFile, []byte{}, 0o644)
		require.NoError(t, err)

		body := PostFilesDownloadInitJSONBody{
			Path: testFile,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})

		resp := w.Result()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var result MultipartDownloadInit
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.Equal(t, int64(0), result.TotalSize)
		assert.Equal(t, 0, result.NumParts)
	})

	t.Run("max sessions limit returns 429", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		err := os.WriteFile(testFile, []byte("test"), 0o644)
		require.NoError(t, err)

		// Fill up the sessions
		for i := 0; i < maxDownloadSessions; i++ {
			body := PostFilesDownloadInitJSONBody{
				Path: testFile,
			}
			bodyBytes, err := json.Marshal(body)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})
			require.Equal(t, http.StatusOK, w.Result().StatusCode)
		}

		// Try to create one more
		body := PostFilesDownloadInitJSONBody{
			Path: testFile,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})

		resp := w.Result()
		assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	})
}

func TestGetFilesDownloadDownloadId(t *testing.T) {
	t.Parallel()

	t.Run("download all parts and verify content", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		testContent := []byte("Hello, World! This is test content for download that spans multiple parts.")
		err := os.WriteFile(testFile, testContent, 0o644)
		require.NoError(t, err)

		// Init session with small part size
		partSize := int64(20)
		body := PostFilesDownloadInitJSONBody{
			Path:     testFile,
			PartSize: &partSize,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})
		require.Equal(t, http.StatusOK, w.Result().StatusCode)

		var initResult MultipartDownloadInit
		err = json.NewDecoder(w.Result().Body).Decode(&initResult)
		require.NoError(t, err)

		// Download all parts
		var downloadedContent bytes.Buffer
		for i := 0; i < initResult.NumParts; i++ {
			req := httptest.NewRequest(http.MethodGet, "/files/download/"+initResult.DownloadId.String()+"?part="+strconv.Itoa(i), nil)
			w := httptest.NewRecorder()

			api.GetFilesDownloadDownloadId(w, req, initResult.DownloadId, GetFilesDownloadDownloadIdParams{Part: i})

			resp := w.Result()
			require.Equal(t, http.StatusOK, resp.StatusCode, "part %d failed", i)

			// Verify headers
			partNum, _ := strconv.Atoi(resp.Header.Get("X-Part-Number"))
			assert.Equal(t, i, partNum)

			partData, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			downloadedContent.Write(partData)
		}

		assert.Equal(t, testContent, downloadedContent.Bytes())
	})

	t.Run("out-of-order part downloads work", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		testContent := []byte("AAAABBBBCCCCDDDD")
		err := os.WriteFile(testFile, testContent, 0o644)
		require.NoError(t, err)

		// Init session with part size of 4
		partSize := int64(4)
		body := PostFilesDownloadInitJSONBody{
			Path:     testFile,
			PartSize: &partSize,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})
		require.Equal(t, http.StatusOK, w.Result().StatusCode)

		var initResult MultipartDownloadInit
		err = json.NewDecoder(w.Result().Body).Decode(&initResult)
		require.NoError(t, err)
		require.Equal(t, 4, initResult.NumParts)

		// Download parts out of order: 2, 0, 3, 1
		downloadOrder := []int{2, 0, 3, 1}
		parts := make([][]byte, 4)

		for _, partNum := range downloadOrder {
			req := httptest.NewRequest(http.MethodGet, "/files/download/"+initResult.DownloadId.String()+"?part="+strconv.Itoa(partNum), nil)
			w := httptest.NewRecorder()

			api.GetFilesDownloadDownloadId(w, req, initResult.DownloadId, GetFilesDownloadDownloadIdParams{Part: partNum})

			resp := w.Result()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			partData, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			parts[partNum] = partData
		}

		// Verify content
		assert.Equal(t, []byte("AAAA"), parts[0])
		assert.Equal(t, []byte("BBBB"), parts[1])
		assert.Equal(t, []byte("CCCC"), parts[2])
		assert.Equal(t, []byte("DDDD"), parts[3])
	})

	t.Run("non-existent session returns 404", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		fakeID := openapi_types.UUID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}

		req := httptest.NewRequest(http.MethodGet, "/files/download/"+fakeID.String()+"?part=0", nil)
		w := httptest.NewRecorder()

		api.GetFilesDownloadDownloadId(w, req, fakeID, GetFilesDownloadDownloadIdParams{Part: 0})

		resp := w.Result()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("part number out of range returns 400", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		err := os.WriteFile(testFile, []byte("test"), 0o644)
		require.NoError(t, err)

		// Init session
		body := PostFilesDownloadInitJSONBody{
			Path: testFile,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})
		require.Equal(t, http.StatusOK, w.Result().StatusCode)

		var initResult MultipartDownloadInit
		err = json.NewDecoder(w.Result().Body).Decode(&initResult)
		require.NoError(t, err)

		// Try to download part 99
		req = httptest.NewRequest(http.MethodGet, "/files/download/"+initResult.DownloadId.String()+"?part=99", nil)
		w = httptest.NewRecorder()

		api.GetFilesDownloadDownloadId(w, req, initResult.DownloadId, GetFilesDownloadDownloadIdParams{Part: 99})

		resp := w.Result()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("negative part number returns 400", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		err := os.WriteFile(testFile, []byte("test"), 0o644)
		require.NoError(t, err)

		// Init session
		body := PostFilesDownloadInitJSONBody{
			Path: testFile,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})
		require.Equal(t, http.StatusOK, w.Result().StatusCode)

		var initResult MultipartDownloadInit
		err = json.NewDecoder(w.Result().Body).Decode(&initResult)
		require.NoError(t, err)

		// Try to download part -1
		req = httptest.NewRequest(http.MethodGet, "/files/download/"+initResult.DownloadId.String()+"?part=-1", nil)
		w = httptest.NewRecorder()

		api.GetFilesDownloadDownloadId(w, req, initResult.DownloadId, GetFilesDownloadDownloadIdParams{Part: -1})

		resp := w.Result()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestDeleteFilesDownloadDownloadId(t *testing.T) {
	t.Parallel()

	t.Run("delete session succeeds", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		err := os.WriteFile(testFile, []byte("test"), 0o644)
		require.NoError(t, err)

		// Init session
		body := PostFilesDownloadInitJSONBody{
			Path: testFile,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})
		require.Equal(t, http.StatusOK, w.Result().StatusCode)

		var initResult MultipartDownloadInit
		err = json.NewDecoder(w.Result().Body).Decode(&initResult)
		require.NoError(t, err)

		// Delete session
		req = httptest.NewRequest(http.MethodDelete, "/files/download/"+initResult.DownloadId.String(), nil)
		w = httptest.NewRecorder()

		api.DeleteFilesDownloadDownloadId(w, req, initResult.DownloadId)

		resp := w.Result()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		// Verify session is gone
		api.downloadsLock.RLock()
		_, exists := api.downloads[initResult.DownloadId.String()]
		api.downloadsLock.RUnlock()
		assert.False(t, exists)
	})

	t.Run("delete non-existent session returns 404", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		fakeID := openapi_types.UUID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}

		req := httptest.NewRequest(http.MethodDelete, "/files/download/"+fakeID.String(), nil)
		w := httptest.NewRecorder()

		api.DeleteFilesDownloadDownloadId(w, req, fakeID)

		resp := w.Result()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("cannot download after delete", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		err := os.WriteFile(testFile, []byte("test content"), 0o644)
		require.NoError(t, err)

		// Init session
		body := PostFilesDownloadInitJSONBody{
			Path: testFile,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})
		require.Equal(t, http.StatusOK, w.Result().StatusCode)

		var initResult MultipartDownloadInit
		err = json.NewDecoder(w.Result().Body).Decode(&initResult)
		require.NoError(t, err)

		// Delete session
		req = httptest.NewRequest(http.MethodDelete, "/files/download/"+initResult.DownloadId.String(), nil)
		w = httptest.NewRecorder()

		api.DeleteFilesDownloadDownloadId(w, req, initResult.DownloadId)
		require.Equal(t, http.StatusNoContent, w.Result().StatusCode)

		// Try to download after delete
		req = httptest.NewRequest(http.MethodGet, "/files/download/"+initResult.DownloadId.String()+"?part=0", nil)
		w = httptest.NewRecorder()

		api.GetFilesDownloadDownloadId(w, req, initResult.DownloadId, GetFilesDownloadDownloadIdParams{Part: 0})

		resp := w.Result()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestCleanupExpiredDownloads(t *testing.T) {
	t.Parallel()

	t.Run("expired sessions are cleaned up", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		err := os.WriteFile(testFile, []byte("test"), 0o644)
		require.NoError(t, err)

		// Init session
		body := PostFilesDownloadInitJSONBody{
			Path: testFile,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesDownloadInit(w, req, PostFilesDownloadInitParams{})
		require.Equal(t, http.StatusOK, w.Result().StatusCode)

		var initResult MultipartDownloadInit
		err = json.NewDecoder(w.Result().Body).Decode(&initResult)
		require.NoError(t, err)

		// Manually set the session to be expired
		api.downloadsLock.Lock()
		session := api.downloads[initResult.DownloadId.String()]
		session.CreatedAt = time.Now().Add(-2 * downloadSessionTTL)
		api.downloadsLock.Unlock()

		// Manually trigger cleanup
		api.downloadsLock.Lock()
		now := time.Now()
		for id, s := range api.downloads {
			if now.Sub(s.CreatedAt) > downloadSessionTTL {
				if s.closed.CompareAndSwap(false, true) {
					s.SrcFile.Close()
					delete(api.downloads, id)
				}
			}
		}
		api.downloadsLock.Unlock()

		// Verify session is gone
		api.downloadsLock.RLock()
		_, exists := api.downloads[initResult.DownloadId.String()]
		api.downloadsLock.RUnlock()
		assert.False(t, exists)
	})
}

func TestAuthAllowlistForDownload(t *testing.T) {
	t.Parallel()

	t.Run("download init path is allowed", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, allowedExactPaths, "POST/files/download/init")
	})

	t.Run("download path prefix is allowed", func(t *testing.T) {
		t.Parallel()
		assert.True(t, hasAllowedPathPrefix("GET/files/download/some-uuid"))
		assert.True(t, hasAllowedPathPrefix("DELETE/files/download/some-uuid"))
	})

	t.Run("other paths are not allowed", func(t *testing.T) {
		t.Parallel()
		assert.False(t, hasAllowedPathPrefix("POST/files/download/some-uuid"))
		assert.False(t, hasAllowedPathPrefix("GET/other/path"))
	})
}

// Integration test that uses the chi router
func TestMultipartDownloadIntegration(t *testing.T) {
	t.Parallel()

	t.Run("full download flow through router", func(t *testing.T) {
		t.Parallel()
		api := setupTestAPIForDownload(t)

		// Create a test file
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "testfile.txt")
		testContent := []byte("Integration test content!")
		err := os.WriteFile(testFile, testContent, 0o644)
		require.NoError(t, err)

		// Create router
		r := chi.NewRouter()
		HandlerFromMux(api, r)

		// Init session
		body := PostFilesDownloadInitJSONBody{
			Path: testFile,
		}
		bodyBytes, err := json.Marshal(body)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/files/download/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Result().StatusCode)

		var initResult MultipartDownloadInit
		err = json.NewDecoder(w.Result().Body).Decode(&initResult)
		require.NoError(t, err)

		// Download part 0
		req = httptest.NewRequest(http.MethodGet, "/files/download/"+initResult.DownloadId.String()+"?part=0", nil)
		w = httptest.NewRecorder()

		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Result().StatusCode)

		partData, err := io.ReadAll(w.Result().Body)
		require.NoError(t, err)
		assert.Equal(t, testContent, partData)

		// Delete session
		req = httptest.NewRequest(http.MethodDelete, "/files/download/"+initResult.DownloadId.String(), nil)
		w = httptest.NewRecorder()

		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Result().StatusCode)
	})
}
