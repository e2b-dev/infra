package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

func newMultipartTestAPI(t *testing.T) (*API, *user.User) {
	t.Helper()

	currentUser, err := user.Current()
	require.NoError(t, err)

	logger := zerolog.Nop()
	defaults := &execcontext.Defaults{
		EnvVars: utils.NewMap[string, string](),
		User:    currentUser.Username,
	}

	return New(&logger, defaults, nil, false), currentUser
}

func initUpload(t *testing.T, api *API, destPath string, username string) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/files/upload/init?path="+url.QueryEscape(destPath), nil)
	w := httptest.NewRecorder()

	params := PostFilesUploadInitParams{
		Path:     &destPath,
		Username: &username,
	}
	api.PostFilesUploadInit(w, req, params)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result UploadInit
	err := json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	require.NotEmpty(t, result.UploadId)

	return result.UploadId
}

func uploadPart(t *testing.T, api *API, uploadId string, partNumber int, data []byte) UploadPartInfo {
	t.Helper()

	req := httptest.NewRequest(http.MethodPut, "/files/upload/"+uploadId+"?partNumber="+url.QueryEscape(string(rune('0'+partNumber))), bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()

	params := PutFilesUploadUploadIdParams{PartNumber: partNumber}
	api.PutFilesUploadUploadId(w, req, uploadId, params)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result UploadPartInfo
	err := json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	return result
}

func TestMultipartUpload_InitCreatesSession(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "test-file.txt")

	uploadId := initUpload(t, api, destPath, currentUser.Username)

	// Verify session exists in memory
	meta, err := api.getUpload(uploadId)
	require.NoError(t, err)
	assert.Equal(t, destPath, meta.Path)

	// Verify temp directory was created
	_, err = os.Stat(uploadDir(uploadId))
	require.NoError(t, err)

	// Cleanup
	os.RemoveAll(uploadDir(uploadId))
}

func TestMultipartUpload_InitRequiresPath(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/files/upload/init", nil)
	w := httptest.NewRecorder()

	params := PostFilesUploadInitParams{
		Username: &currentUser.Username,
	}
	api.PostFilesUploadInit(w, req, params)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestMultipartUpload_PutPart(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "test-file.txt")

	uploadId := initUpload(t, api, destPath, currentUser.Username)
	defer os.RemoveAll(uploadDir(uploadId))

	content := []byte("hello world")
	result := uploadPart(t, api, uploadId, 0, content)

	assert.Equal(t, 0, result.PartNumber)
	assert.Equal(t, int64(len(content)), result.Size)

	// Verify part file exists with correct content
	partData, err := os.ReadFile(filepath.Join(uploadDir(uploadId), "000000"))
	require.NoError(t, err)
	assert.Equal(t, content, partData)
}

func TestMultipartUpload_PutPartNotFound(t *testing.T) {
	t.Parallel()

	api, _ := newMultipartTestAPI(t)

	req := httptest.NewRequest(http.MethodPut, "/files/upload/nonexistent?partNumber=0", bytes.NewReader([]byte("data")))
	w := httptest.NewRecorder()

	params := PutFilesUploadUploadIdParams{PartNumber: 0}
	api.PutFilesUploadUploadId(w, req, "nonexistent", params)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestMultipartUpload_CompleteAssemblesFile(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "assembled-file.txt")

	uploadId := initUpload(t, api, destPath, currentUser.Username)

	// Upload 3 parts
	part0 := []byte("Hello, ")
	part1 := []byte("World")
	part2 := []byte("!")

	uploadPart(t, api, uploadId, 0, part0)
	uploadPart(t, api, uploadId, 1, part1)
	uploadPart(t, api, uploadId, 2, part2)

	// Complete the upload
	req := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
	w := httptest.NewRecorder()

	api.PostFilesUploadUploadIdComplete(w, req, uploadId)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result UploadComplete
	err := json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	assert.Equal(t, destPath, result.Path)
	assert.Equal(t, int64(len(part0)+len(part1)+len(part2)), result.Size)

	// Verify assembled file content
	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("Hello, World!"), data)

	// Verify temp directory was cleaned up
	_, err = os.Stat(uploadDir(uploadId))
	assert.True(t, os.IsNotExist(err))

	// Verify session was removed from memory
	_, err = api.getUpload(uploadId)
	assert.Error(t, err)
}

func TestMultipartUpload_CompleteNotFound(t *testing.T) {
	t.Parallel()

	api, _ := newMultipartTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/files/upload/nonexistent/complete", nil)
	w := httptest.NewRecorder()

	api.PostFilesUploadUploadIdComplete(w, req, "nonexistent")

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestMultipartUpload_CompleteCreatesParentDirs(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "nested", "dir", "file.txt")

	uploadId := initUpload(t, api, destPath, currentUser.Username)

	uploadPart(t, api, uploadId, 0, []byte("content"))

	req := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
	w := httptest.NewRecorder()

	api.PostFilesUploadUploadIdComplete(w, req, uploadId)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("content"), data)
}

func TestMultipartUpload_Abort(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "test-file.txt")

	uploadId := initUpload(t, api, destPath, currentUser.Username)

	// Upload a part
	uploadPart(t, api, uploadId, 0, []byte("data"))

	// Abort
	req := httptest.NewRequest(http.MethodDelete, "/files/upload/"+uploadId, nil)
	w := httptest.NewRecorder()

	api.DeleteFilesUploadUploadId(w, req, uploadId)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify temp directory was cleaned up
	_, err := os.Stat(uploadDir(uploadId))
	assert.True(t, os.IsNotExist(err))

	// Verify session was removed from memory
	_, err = api.getUpload(uploadId)
	assert.Error(t, err)
}

func TestMultipartUpload_AbortNotFound(t *testing.T) {
	t.Parallel()

	api, _ := newMultipartTestAPI(t)

	req := httptest.NewRequest(http.MethodDelete, "/files/upload/nonexistent", nil)
	w := httptest.NewRecorder()

	api.DeleteFilesUploadUploadId(w, req, "nonexistent")

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestMultipartUpload_ReuploadPart(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "test-file.txt")

	uploadId := initUpload(t, api, destPath, currentUser.Username)
	defer os.RemoveAll(uploadDir(uploadId))

	// Upload part 0
	uploadPart(t, api, uploadId, 0, []byte("original"))

	// Re-upload part 0 with different content
	uploadPart(t, api, uploadId, 0, []byte("replaced"))

	// Verify the part was overwritten
	partData, err := os.ReadFile(filepath.Join(uploadDir(uploadId), "000000"))
	require.NoError(t, err)
	assert.Equal(t, []byte("replaced"), partData)
}

func TestMultipartUpload_LargeFile(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "large-file.bin")

	uploadId := initUpload(t, api, destPath, currentUser.Username)

	// Upload 10 parts of 1MB each
	partSize := 1024 * 1024
	expectedTotal := int64(0)

	for i := range 10 {
		data := make([]byte, partSize)
		for j := range data {
			data[j] = byte(i)
		}

		result := uploadPart(t, api, uploadId, i, data)
		assert.Equal(t, int64(partSize), result.Size)

		expectedTotal += int64(partSize)
	}

	// Complete
	req := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
	w := httptest.NewRecorder()

	api.PostFilesUploadUploadIdComplete(w, req, uploadId)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result UploadComplete
	err := json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	assert.Equal(t, expectedTotal, result.Size)

	// Verify file content
	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Len(t, data, int(expectedTotal))

	// Verify each part's content
	for i := range 10 {
		offset := i * partSize
		for j := range partSize {
			assert.Equal(t, byte(i), data[offset+j], "byte mismatch at part %d offset %d", i, j)
		}
	}
}

func TestMultipartUpload_EmptyUpload(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "empty-file.txt")

	uploadId := initUpload(t, api, destPath, currentUser.Username)

	// Complete with no parts
	req := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
	w := httptest.NewRecorder()

	api.PostFilesUploadUploadIdComplete(w, req, uploadId)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result UploadComplete
	err := json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	assert.Equal(t, int64(0), result.Size)

	// Verify empty file was created
	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestMultipartUpload_NonSequentialParts(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "non-sequential.txt")

	uploadId := initUpload(t, api, destPath, currentUser.Username)

	// Upload parts out of order with gaps
	uploadPart(t, api, uploadId, 5, []byte("part5"))
	uploadPart(t, api, uploadId, 2, []byte("part2"))
	uploadPart(t, api, uploadId, 0, []byte("part0"))

	// Complete
	req := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
	w := httptest.NewRecorder()

	api.PostFilesUploadUploadIdComplete(w, req, uploadId)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Parts should be assembled in sorted order: 0, 2, 5
	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("part0part2part5"), data)
}

func TestMultipartUpload_CompleteRoundTripWithDownload(t *testing.T) {
	t.Parallel()

	api, currentUser := newMultipartTestAPI(t)
	destPath := filepath.Join(t.TempDir(), "roundtrip.txt")

	// Upload via multipart
	uploadId := initUpload(t, api, destPath, currentUser.Username)

	uploadPart(t, api, uploadId, 0, []byte("round"))
	uploadPart(t, api, uploadId, 1, []byte("trip"))

	completeReq := httptest.NewRequest(http.MethodPost, "/files/upload/"+uploadId+"/complete", nil)
	completeW := httptest.NewRecorder()
	api.PostFilesUploadUploadIdComplete(completeW, completeReq, uploadId)
	require.Equal(t, http.StatusOK, completeW.Result().StatusCode)

	// Download via GetFiles
	downloadReq := httptest.NewRequest(http.MethodGet, "/files?path="+url.QueryEscape(destPath), nil)
	downloadW := httptest.NewRecorder()

	downloadParams := GetFilesParams{
		Path:     &destPath,
		Username: &currentUser.Username,
	}
	api.GetFiles(downloadW, downloadReq, downloadParams)

	downloadResp := downloadW.Result()
	defer downloadResp.Body.Close()

	require.Equal(t, http.StatusOK, downloadResp.StatusCode)

	body, err := io.ReadAll(downloadResp.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("roundtrip"), body)
}
