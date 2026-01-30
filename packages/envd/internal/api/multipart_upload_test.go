package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

func newTestAPI(t *testing.T) *API {
	t.Helper()
	logger := zerolog.New(os.Stderr).Level(zerolog.Disabled)
	defaults := &execcontext.Defaults{
		User:    "root",
		EnvVars: utils.NewMap[string, string](),
	}

	return New(&logger, defaults, nil, true)
}

func TestMultipartUpload(t *testing.T) {
	t.Parallel()

	// Skip if not running as root (needed for user lookup and chown)
	if os.Geteuid() != 0 {
		t.Skip("skipping multipart upload tests: requires root")
	}

	t.Run("init upload", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)
		tempDir := t.TempDir()

		body := PostFilesUploadInitJSONRequestBody{
			Path:      filepath.Join(tempDir, "test-file.txt"),
			TotalSize: 100,
			PartSize:  50,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesUploadInit(w, req, PostFilesUploadInitParams{})

		assert.Equal(t, http.StatusOK, w.Code)

		var resp MultipartUploadInit
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.UploadId)

		// Clean up
		api.uploadsLock.Lock()
		session := api.uploads[resp.UploadId]
		if session != nil {
			session.DestFile.Close()
			os.Remove(session.FilePath)
		}
		delete(api.uploads, resp.UploadId)
		api.uploadsLock.Unlock()
	})

	t.Run("complete multipart upload", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "assembled-file.txt")

		part0Content := []byte("Hello, ")
		part1Content := []byte("World!")
		totalSize := int64(len(part0Content) + len(part1Content))
		partSize := int64(len(part0Content))

		// Initialize upload
		initBody := PostFilesUploadInitJSONRequestBody{
			Path:      destPath,
			TotalSize: totalSize,
			PartSize:  partSize,
		}
		initBodyBytes, _ := json.Marshal(initBody)

		initReq := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(initBodyBytes))
		initReq.Header.Set("Content-Type", "application/json")
		initW := httptest.NewRecorder()

		api.PostFilesUploadInit(initW, initReq, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, initW.Code)

		var initResp MultipartUploadInit
		err := json.Unmarshal(initW.Body.Bytes(), &initResp)
		require.NoError(t, err)
		uploadId := initResp.UploadId

		// Upload part 0
		part0Req := httptest.NewRequest(http.MethodPut, "/files/upload/"+uploadId+"?part=0", bytes.NewReader(part0Content))
		part0Req.Header.Set("Content-Type", "application/octet-stream")
		part0W := httptest.NewRecorder()

		api.PutFilesUploadUploadId(part0W, part0Req, uploadId, PutFilesUploadUploadIdParams{Part: 0})
		require.Equal(t, http.StatusOK, part0W.Code)

		var part0Resp MultipartUploadPart
		err = json.Unmarshal(part0W.Body.Bytes(), &part0Resp)
		require.NoError(t, err)
		assert.Equal(t, 0, part0Resp.PartNumber)
		assert.Equal(t, int64(len(part0Content)), part0Resp.Size)

		// Upload part 1
		part1Req := httptest.NewRequest(http.MethodPut, "/files/upload/"+uploadId+"?part=1", bytes.NewReader(part1Content))
		part1Req.Header.Set("Content-Type", "application/octet-stream")
		part1W := httptest.NewRecorder()

		api.PutFilesUploadUploadId(part1W, part1Req, uploadId, PutFilesUploadUploadIdParams{Part: 1})
		require.Equal(t, http.StatusOK, part1W.Code)

		// Complete upload
		completeReq := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
		completeW := httptest.NewRecorder()

		api.PostFilesUploadUploadIdComplete(completeW, completeReq, uploadId)
		require.Equal(t, http.StatusOK, completeW.Code)

		var completeResp MultipartUploadComplete
		err = json.Unmarshal(completeW.Body.Bytes(), &completeResp)
		require.NoError(t, err)
		assert.Equal(t, destPath, completeResp.Path)
		assert.Equal(t, totalSize, completeResp.Size)

		// Verify file contents
		content, err := os.ReadFile(destPath)
		require.NoError(t, err)
		assert.Equal(t, "Hello, World!", string(content))
	})

	t.Run("abort multipart upload", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "aborted-file.txt")

		// Initialize upload
		initBody := PostFilesUploadInitJSONRequestBody{
			Path:      destPath,
			TotalSize: 100,
			PartSize:  50,
		}
		initBodyBytes, _ := json.Marshal(initBody)

		initReq := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(initBodyBytes))
		initReq.Header.Set("Content-Type", "application/json")
		initW := httptest.NewRecorder()

		api.PostFilesUploadInit(initW, initReq, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, initW.Code)

		var initResp MultipartUploadInit
		err := json.Unmarshal(initW.Body.Bytes(), &initResp)
		require.NoError(t, err)
		uploadId := initResp.UploadId

		// Verify file was created
		_, err = os.Stat(destPath)
		require.NoError(t, err, "destination file should exist after init")

		// Abort upload
		abortReq := httptest.NewRequest(http.MethodDelete, "/files/upload/"+uploadId, nil)
		abortW := httptest.NewRecorder()

		api.DeleteFilesUploadUploadId(abortW, abortReq, uploadId)
		assert.Equal(t, http.StatusNoContent, abortW.Code)

		// Verify session is removed
		api.uploadsLock.RLock()
		_, exists := api.uploads[uploadId]
		api.uploadsLock.RUnlock()
		assert.False(t, exists)

		// Verify file is cleaned up
		_, err = os.Stat(destPath)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("upload part to non-existent session", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)

		// Use a valid UUID that doesn't exist in the sessions map
		nonExistentUUID := "00000000-0000-0000-0000-000000000000"
		req := httptest.NewRequest(http.MethodPut, "/files/upload/"+nonExistentUUID+"?part=0", bytes.NewReader([]byte("test")))
		w := httptest.NewRecorder()

		api.PutFilesUploadUploadId(w, req, nonExistentUUID, PutFilesUploadUploadIdParams{Part: 0})
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("complete non-existent session", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)

		req := httptest.NewRequest(http.MethodPost, "/files/upload/non-existent/complete", nil)
		w := httptest.NewRecorder()

		api.PostFilesUploadUploadIdComplete(w, req, "non-existent")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("abort non-existent session", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)

		req := httptest.NewRequest(http.MethodDelete, "/files/upload/non-existent", nil)
		w := httptest.NewRecorder()

		api.DeleteFilesUploadUploadId(w, req, "non-existent")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid upload ID format", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)

		// Try to upload with an invalid UUID (path traversal attempt)
		req := httptest.NewRequest(http.MethodPut, "/files/upload/../../../etc/passwd?part=0", bytes.NewReader([]byte("test")))
		w := httptest.NewRecorder()

		api.PutFilesUploadUploadId(w, req, "../../../etc/passwd", PutFilesUploadUploadIdParams{Part: 0})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("negative part number", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)
		tempDir := t.TempDir()

		// Initialize upload
		body := PostFilesUploadInitJSONRequestBody{
			Path:      filepath.Join(tempDir, "test-file.txt"),
			TotalSize: 100,
			PartSize:  50,
		}
		bodyBytes, _ := json.Marshal(body)

		initReq := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		initReq.Header.Set("Content-Type", "application/json")
		initW := httptest.NewRecorder()

		api.PostFilesUploadInit(initW, initReq, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, initW.Code)

		var initResp MultipartUploadInit
		err := json.Unmarshal(initW.Body.Bytes(), &initResp)
		require.NoError(t, err)
		uploadId := initResp.UploadId

		// Try to upload with negative part number
		req := httptest.NewRequest(http.MethodPut, "/files/upload/"+uploadId+"?part=-1", bytes.NewReader([]byte("test")))
		w := httptest.NewRecorder()

		api.PutFilesUploadUploadId(w, req, uploadId, PutFilesUploadUploadIdParams{Part: -1})
		assert.Equal(t, http.StatusBadRequest, w.Code)

		// Clean up
		api.uploadsLock.Lock()
		session := api.uploads[uploadId]
		if session != nil {
			session.DestFile.Close()
			os.Remove(session.FilePath)
		}
		delete(api.uploads, uploadId)
		api.uploadsLock.Unlock()
	})

	t.Run("missing part in sequence", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "gap-file.txt")

		// Initialize upload with 3 parts
		body := PostFilesUploadInitJSONRequestBody{
			Path:      destPath,
			TotalSize: 30,
			PartSize:  10,
		}
		bodyBytes, _ := json.Marshal(body)

		initReq := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		initReq.Header.Set("Content-Type", "application/json")
		initW := httptest.NewRecorder()

		api.PostFilesUploadInit(initW, initReq, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, initW.Code)

		var initResp MultipartUploadInit
		err := json.Unmarshal(initW.Body.Bytes(), &initResp)
		require.NoError(t, err)
		uploadId := initResp.UploadId

		// Upload parts 0 and 2, but skip part 1
		for _, partNum := range []int{0, 2} {
			content := make([]byte, 10)
			partReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/files/upload/%s?part=%d", uploadId, partNum), bytes.NewReader(content))
			partReq.Header.Set("Content-Type", "application/octet-stream")
			partW := httptest.NewRecorder()

			api.PutFilesUploadUploadId(partW, partReq, uploadId, PutFilesUploadUploadIdParams{Part: partNum})
			require.Equal(t, http.StatusOK, partW.Code)
		}

		// Complete should fail due to missing part 1
		completeReq := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
		completeW := httptest.NewRecorder()

		api.PostFilesUploadUploadIdComplete(completeW, completeReq, uploadId)
		assert.Equal(t, http.StatusBadRequest, completeW.Code)
	})

	t.Run("upload part after complete started", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "race-file.txt")

		// Initialize upload
		body := PostFilesUploadInitJSONRequestBody{
			Path:      destPath,
			TotalSize: 10,
			PartSize:  10,
		}
		bodyBytes, _ := json.Marshal(body)

		initReq := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		initReq.Header.Set("Content-Type", "application/json")
		initW := httptest.NewRecorder()

		api.PostFilesUploadInit(initW, initReq, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, initW.Code)

		var initResp MultipartUploadInit
		err := json.Unmarshal(initW.Body.Bytes(), &initResp)
		require.NoError(t, err)
		uploadId := initResp.UploadId

		// Upload part 0
		part0Content := make([]byte, 10)
		part0Req := httptest.NewRequest(http.MethodPut, "/files/upload/"+uploadId+"?part=0", bytes.NewReader(part0Content))
		part0Req.Header.Set("Content-Type", "application/octet-stream")
		part0W := httptest.NewRecorder()

		api.PutFilesUploadUploadId(part0W, part0Req, uploadId, PutFilesUploadUploadIdParams{Part: 0})
		require.Equal(t, http.StatusOK, part0W.Code)

		// Mark the session as completing
		api.uploadsLock.RLock()
		session := api.uploads[uploadId]
		api.uploadsLock.RUnlock()
		require.NotNil(t, session)
		session.completed.Store(true)

		// Try to upload another part - should fail with 409 Conflict
		part1Content := make([]byte, 10)
		part1Req := httptest.NewRequest(http.MethodPut, "/files/upload/"+uploadId+"?part=0", bytes.NewReader(part1Content))
		part1Req.Header.Set("Content-Type", "application/octet-stream")
		part1W := httptest.NewRecorder()

		api.PutFilesUploadUploadId(part1W, part1Req, uploadId, PutFilesUploadUploadIdParams{Part: 0})
		assert.Equal(t, http.StatusConflict, part1W.Code)

		// Clean up
		api.uploadsLock.Lock()
		delete(api.uploads, uploadId)
		api.uploadsLock.Unlock()
		session.DestFile.Close()
		os.Remove(destPath)
	})

	t.Run("max sessions limit", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)
		tempDir := t.TempDir()

		// Create maxUploadSessions sessions
		for i := range maxUploadSessions {
			body := PostFilesUploadInitJSONRequestBody{
				Path:      filepath.Join(tempDir, fmt.Sprintf("file-%d.txt", i)),
				TotalSize: 100,
				PartSize:  50,
			}
			bodyBytes, _ := json.Marshal(body)

			req := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			api.PostFilesUploadInit(w, req, PostFilesUploadInitParams{})
			require.Equal(t, http.StatusOK, w.Code, "session %d should succeed", i)
		}

		// The next one should fail with 429
		body := PostFilesUploadInitJSONRequestBody{
			Path:      filepath.Join(tempDir, "one-too-many.txt"),
			TotalSize: 100,
			PartSize:  50,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesUploadInit(w, req, PostFilesUploadInitParams{})
		assert.Equal(t, http.StatusTooManyRequests, w.Code)

		// Clean up all sessions
		api.uploadsLock.Lock()
		for _, session := range api.uploads {
			session.DestFile.Close()
			os.Remove(session.FilePath)
		}
		api.uploads = make(map[string]*MultipartUploadSession)
		api.uploadsLock.Unlock()
	})

	t.Run("parts uploaded out of order", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "out-of-order-file.txt")

		// Initialize upload with 3 parts of 1 byte each
		body := PostFilesUploadInitJSONRequestBody{
			Path:      destPath,
			TotalSize: 3,
			PartSize:  1,
		}
		bodyBytes, _ := json.Marshal(body)

		initReq := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		initReq.Header.Set("Content-Type", "application/json")
		initW := httptest.NewRecorder()

		api.PostFilesUploadInit(initW, initReq, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, initW.Code)

		var initResp MultipartUploadInit
		err := json.Unmarshal(initW.Body.Bytes(), &initResp)
		require.NoError(t, err)
		uploadId := initResp.UploadId

		// Upload parts out of order (part 2 first, then 0, then 1)
		parts := []struct {
			num     int
			content string
		}{
			{2, "C"},
			{0, "A"},
			{1, "B"},
		}

		for _, part := range parts {
			partReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/files/upload/%s?part=%d", uploadId, part.num), bytes.NewReader([]byte(part.content)))
			partReq.Header.Set("Content-Type", "application/octet-stream")
			partW := httptest.NewRecorder()

			api.PutFilesUploadUploadId(partW, partReq, uploadId, PutFilesUploadUploadIdParams{Part: part.num})
			require.Equal(t, http.StatusOK, partW.Code)
		}

		// Complete upload
		completeReq := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
		completeW := httptest.NewRecorder()

		api.PostFilesUploadUploadIdComplete(completeW, completeReq, uploadId)
		require.Equal(t, http.StatusOK, completeW.Code)

		// Verify file contents are assembled in order
		content, err := os.ReadFile(destPath)
		require.NoError(t, err)
		assert.Equal(t, "ABC", string(content))
	})

	t.Run("empty file upload", func(t *testing.T) {
		t.Parallel()
		api := newTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "empty-file.txt")

		// Initialize upload with 0 size
		body := PostFilesUploadInitJSONRequestBody{
			Path:      destPath,
			TotalSize: 0,
			PartSize:  1024,
		}
		bodyBytes, _ := json.Marshal(body)

		initReq := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		initReq.Header.Set("Content-Type", "application/json")
		initW := httptest.NewRecorder()

		api.PostFilesUploadInit(initW, initReq, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, initW.Code)

		var initResp MultipartUploadInit
		err := json.Unmarshal(initW.Body.Bytes(), &initResp)
		require.NoError(t, err)
		uploadId := initResp.UploadId

		// Complete upload (no parts needed)
		completeReq := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
		completeW := httptest.NewRecorder()

		api.PostFilesUploadUploadIdComplete(completeW, completeReq, uploadId)
		require.Equal(t, http.StatusOK, completeW.Code)

		// Verify file exists and is empty
		content, err := os.ReadFile(destPath)
		require.NoError(t, err)
		assert.Empty(t, string(content))
	})
}

func TestMultipartUploadRouting(t *testing.T) {
	t.Parallel()

	// Skip if not running as root
	if os.Geteuid() != 0 {
		t.Skip("skipping routing tests: requires root")
	}

	api := newTestAPI(t)
	router := chi.NewRouter()
	HandlerFromMux(api, router)

	// Test that routes are registered
	t.Run("init route exists", func(t *testing.T) {
		t.Parallel()
		body := PostFilesUploadInitJSONRequestBody{
			Path:      "/tmp/test-file.txt",
			TotalSize: 100,
			PartSize:  50,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		// Should get 200 (success) not 404 (route not found)
		assert.NotEqual(t, http.StatusNotFound, w.Code)
	})
}
