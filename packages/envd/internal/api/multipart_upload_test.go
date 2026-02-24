package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

func newMultipartTestAPI(t *testing.T) *API {
	t.Helper()
	logger := zerolog.New(os.Stderr).Level(zerolog.Disabled)
	defaults := &execcontext.Defaults{
		User:    "root",
		EnvVars: utils.NewMap[string, string](),
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return New(ctx, &logger, defaults, nil, true)
}

func TestMultipartUpload(t *testing.T) {
	t.Parallel()

	// Skip if not running as root (needed for user lookup and chown)
	if os.Geteuid() != 0 {
		t.Skip("skipping multipart upload tests: requires root")
	}

	t.Run("init upload", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)
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
		api := newMultipartTestAPI(t)
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
		api := newMultipartTestAPI(t)
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
		api := newMultipartTestAPI(t)

		req := httptest.NewRequest(http.MethodPut, "/files/upload/no-such-session?part=0", bytes.NewReader([]byte("test")))
		w := httptest.NewRecorder()

		api.PutFilesUploadUploadId(w, req, "no-such-session", PutFilesUploadUploadIdParams{Part: 0})
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("complete non-existent session", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)

		req := httptest.NewRequest(http.MethodPost, "/files/upload/non-existent/complete", nil)
		w := httptest.NewRecorder()

		api.PostFilesUploadUploadIdComplete(w, req, "non-existent")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("abort non-existent session", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)

		req := httptest.NewRequest(http.MethodDelete, "/files/upload/non-existent", nil)
		w := httptest.NewRecorder()

		api.DeleteFilesUploadUploadId(w, req, "non-existent")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("missing part in sequence", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)
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

		// Session should still exist (completed flag reset) so client can retry
		api.uploadsLock.RLock()
		session, exists := api.uploads[uploadId]
		api.uploadsLock.RUnlock()
		assert.True(t, exists, "session should still exist after failed complete")
		assert.False(t, session.completed.Load(), "completed flag should be reset")

		// Clean up
		api.uploadsLock.Lock()
		if s := api.uploads[uploadId]; s != nil {
			s.DestFile.Close()
			os.Remove(s.FilePath)
		}
		delete(api.uploads, uploadId)
		api.uploadsLock.Unlock()
	})

	t.Run("upload part after complete started", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)
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
		api := newMultipartTestAPI(t)
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
		api.uploads = make(map[string]*multipartUploadSession)
		api.uploadsLock.Unlock()
	})

	t.Run("parts uploaded out of order", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)
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
		api := newMultipartTestAPI(t)
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

	t.Run("reject too many parts", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)

		// totalSize=10GB, partSize=1 would create ~10 billion parts
		body := PostFilesUploadInitJSONRequestBody{
			Path:      "/tmp/too-many-parts.txt",
			TotalSize: maxTotalSize,
			PartSize:  1,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesUploadInit(w, req, PostFilesUploadInitParams{})
		assert.Equal(t, http.StatusBadRequest, w.Code)

		var errResp Error
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Message, "parts")
	})

	t.Run("reject negative totalSize", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)

		body := PostFilesUploadInitJSONRequestBody{
			Path:      "/tmp/negative-size.txt",
			TotalSize: -1,
			PartSize:  1024,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesUploadInit(w, req, PostFilesUploadInitParams{})
		assert.Equal(t, http.StatusBadRequest, w.Code)

		var errResp Error
		err := json.Unmarshal(w.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Message, "non-negative")
	})

	t.Run("reject partSize zero", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)

		body := PostFilesUploadInitJSONRequestBody{
			Path:      "/tmp/should-not-exist.txt",
			TotalSize: 100,
			PartSize:  0,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesUploadInit(w, req, PostFilesUploadInitParams{})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("reject part upload on empty file", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "empty-reject.txt")

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

		// Try to upload a part — should be rejected with clear message
		partReq := httptest.NewRequest(http.MethodPut, "/files/upload/"+uploadId+"?part=0", bytes.NewReader([]byte("data")))
		partReq.Header.Set("Content-Type", "application/octet-stream")
		partW := httptest.NewRecorder()

		api.PutFilesUploadUploadId(partW, partReq, uploadId, PutFilesUploadUploadIdParams{Part: 0})
		assert.Equal(t, http.StatusBadRequest, partW.Code)

		// Verify error message does not contain a huge number from uint underflow
		var errResp Error
		err = json.Unmarshal(partW.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Message, "empty file")

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

	t.Run("reject negative part number", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "neg-part.txt")

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

		// Try to upload with negative part number
		partReq := httptest.NewRequest(http.MethodPut, "/files/upload/"+uploadId+"?part=-1", bytes.NewReader([]byte("data")))
		partReq.Header.Set("Content-Type", "application/octet-stream")
		partW := httptest.NewRecorder()

		api.PutFilesUploadUploadId(partW, partReq, uploadId, PutFilesUploadUploadIdParams{Part: -1})
		assert.Equal(t, http.StatusBadRequest, partW.Code)

		var errResp Error
		err = json.Unmarshal(partW.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Message, "non-negative")

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

	t.Run("reject duplicate destination path", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "dup-path.txt")

		// First init should succeed
		body := PostFilesUploadInitJSONRequestBody{
			Path:      destPath,
			TotalSize: 100,
			PartSize:  50,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		api.PostFilesUploadInit(w, req, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, w.Code)

		var initResp MultipartUploadInit
		err := json.Unmarshal(w.Body.Bytes(), &initResp)
		require.NoError(t, err)
		uploadId := initResp.UploadId

		// Second init with same path should be rejected with 409
		bodyBytes2, _ := json.Marshal(body)
		req2 := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes2))
		req2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()

		api.PostFilesUploadInit(w2, req2, PostFilesUploadInitParams{})
		assert.Equal(t, http.StatusConflict, w2.Code)

		var errResp Error
		err = json.Unmarshal(w2.Body.Bytes(), &errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Message, "active upload session")

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

	t.Run("reuse path after complete", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "reuse-path.txt")

		// First upload (empty file for simplicity)
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

		// Complete it
		completeReq := httptest.NewRequest(http.MethodPost, "/files/upload/"+initResp.UploadId+"/complete", nil)
		completeW := httptest.NewRecorder()

		api.PostFilesUploadUploadIdComplete(completeW, completeReq, initResp.UploadId)
		require.Equal(t, http.StatusOK, completeW.Code)

		// Second init with same path should succeed now
		bodyBytes2, _ := json.Marshal(body)
		initReq2 := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes2))
		initReq2.Header.Set("Content-Type", "application/json")
		initW2 := httptest.NewRecorder()

		api.PostFilesUploadInit(initW2, initReq2, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, initW2.Code)

		var initResp2 MultipartUploadInit
		err = json.Unmarshal(initW2.Body.Bytes(), &initResp2)
		require.NoError(t, err)

		// Clean up
		api.uploadsLock.Lock()
		session := api.uploads[initResp2.UploadId]
		if session != nil {
			session.DestFile.Close()
			os.Remove(session.FilePath)
		}
		delete(api.uploads, initResp2.UploadId)
		api.uploadsLock.Unlock()
	})

	t.Run("reuse path after abort", func(t *testing.T) {
		t.Parallel()
		api := newMultipartTestAPI(t)
		tempDir := t.TempDir()
		destPath := filepath.Join(tempDir, "reuse-abort.txt")

		// First upload
		body := PostFilesUploadInitJSONRequestBody{
			Path:      destPath,
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

		// Abort it
		abortReq := httptest.NewRequest(http.MethodDelete, "/files/upload/"+initResp.UploadId, nil)
		abortW := httptest.NewRecorder()

		api.DeleteFilesUploadUploadId(abortW, abortReq, initResp.UploadId)
		require.Equal(t, http.StatusNoContent, abortW.Code)

		// Second init with same path should succeed now
		bodyBytes2, _ := json.Marshal(body)
		initReq2 := httptest.NewRequest(http.MethodPost, "/files/upload/init", bytes.NewReader(bodyBytes2))
		initReq2.Header.Set("Content-Type", "application/json")
		initW2 := httptest.NewRecorder()

		api.PostFilesUploadInit(initW2, initReq2, PostFilesUploadInitParams{})
		require.Equal(t, http.StatusOK, initW2.Code)

		// Clean up
		var initResp2 MultipartUploadInit
		err = json.Unmarshal(initW2.Body.Bytes(), &initResp2)
		require.NoError(t, err)

		api.uploadsLock.Lock()
		session := api.uploads[initResp2.UploadId]
		if session != nil {
			session.DestFile.Close()
			os.Remove(session.FilePath)
		}
		delete(api.uploads, initResp2.UploadId)
		api.uploadsLock.Unlock()
	})
}
