package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
)

const (
	multipartUploadBaseDir = "/tmp/multipart-uploads"
	partFileFormat         = "%06d"
)

type uploadMetadata struct {
	Path string
	UID  int
	GID  int
}

func uploadDir(uploadId string) string {
	return filepath.Join(multipartUploadBaseDir, uploadId)
}

func (a *API) getUpload(uploadId string) (*uploadMetadata, error) {
	val, ok := a.uploads.Load(uploadId)
	if !ok {
		return nil, fmt.Errorf("upload session not found")
	}

	meta, ok := val.(*uploadMetadata)
	if !ok {
		return nil, fmt.Errorf("invalid upload session data")
	}

	return meta, nil
}

// claimUpload atomically loads and deletes the upload session so that only
// one concurrent caller can proceed (used by Complete and Delete).
func (a *API) claimUpload(uploadId string) (*uploadMetadata, error) {
	val, ok := a.uploads.LoadAndDelete(uploadId)
	if !ok {
		return nil, fmt.Errorf("upload session not found")
	}

	meta, ok := val.(*uploadMetadata)
	if !ok {
		return nil, fmt.Errorf("invalid upload session data")
	}

	return meta, nil
}

func (a *API) PostFilesUploadInit(w http.ResponseWriter, r *http.Request, params PostFilesUploadInitParams) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	if params.Path == nil || *params.Path == "" {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("path is required"))
		return
	}

	username, err := execcontext.ResolveDefaultUsername(params.Username, a.defaults.User)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("no user specified")
		jsonError(w, http.StatusBadRequest, err)

		return
	}

	u, err := user.Lookup(username)
	if err != nil {
		errMsg := fmt.Errorf("error looking up user '%s': %w", username, err)
		a.logger.Error().Err(errMsg).Str(string(logs.OperationIDKey), operationID).Msg("user lookup failed")
		jsonError(w, http.StatusUnauthorized, errMsg)

		return
	}

	uid, gid, err := permissions.GetUserIdInts(u)
	if err != nil {
		errMsg := fmt.Errorf("error getting user ids: %w", err)
		a.logger.Error().Err(errMsg).Str(string(logs.OperationIDKey), operationID).Msg("failed to get user ids")
		jsonError(w, http.StatusInternalServerError, errMsg)

		return
	}

	filePath, err := permissions.ExpandAndResolve(*params.Path, u, a.defaults.Workdir)
	if err != nil {
		errMsg := fmt.Errorf("error resolving path: %w", err)
		a.logger.Error().Err(errMsg).Str(string(logs.OperationIDKey), operationID).Msg("path resolution failed")
		jsonError(w, http.StatusBadRequest, errMsg)

		return
	}

	uploadId := uuid.New().String()

	err = os.MkdirAll(uploadDir(uploadId), 0o700)
	if err != nil {
		errMsg := fmt.Errorf("error creating upload directory: %w", err)
		a.logger.Error().Err(errMsg).Str(string(logs.OperationIDKey), operationID).Msg("failed to create upload dir")
		jsonError(w, http.StatusInternalServerError, errMsg)

		return
	}

	a.uploads.Store(uploadId, &uploadMetadata{
		Path: filePath,
		UID:  uid,
		GID:  gid,
	})

	a.logger.Info().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Str("path", filePath).
		Str("username", username).
		Msg("Multipart upload initialized")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(UploadInit{UploadId: uploadId})
}

func (a *API) PutFilesUploadUploadId(w http.ResponseWriter, r *http.Request, uploadId UploadId, params PutFilesUploadUploadIdParams) {
	defer r.Body.Close()

	if _, err := a.getUpload(uploadId); err != nil {
		jsonError(w, http.StatusNotFound, err)
		return
	}

	if params.PartNumber < 0 {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("partNumber must be non-negative"))
		return
	}

	partPath := filepath.Join(uploadDir(uploadId), fmt.Sprintf(partFileFormat, params.PartNumber))

	file, err := os.OpenFile(partPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space available"))
			return
		}

		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error creating part file: %w", err))

		return
	}
	defer file.Close()

	written, err := file.ReadFrom(r.Body)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space available"))
			return
		}

		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error writing part file: %w", err))

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(UploadPartInfo{
		PartNumber: params.PartNumber,
		Size:       written,
	})
}

func (a *API) PostFilesUploadUploadIdComplete(w http.ResponseWriter, r *http.Request, uploadId UploadId) {
	defer r.Body.Close()

	// Atomically claim the session so concurrent Complete calls are serialized.
	meta, err := a.claimUpload(uploadId)
	if err != nil {
		jsonError(w, http.StatusNotFound, err)
		return
	}

	dir := uploadDir(uploadId)
	defer os.RemoveAll(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error reading parts directory: %w", err))
		return
	}

	var partNames []string
	for _, entry := range entries {
		if !entry.IsDir() {
			partNames = append(partNames, entry.Name())
		}
	}

	// Sort numerically so part ordering is correct even when filenames
	// have different digit counts (e.g. "999999" vs "1000000").
	sort.Slice(partNames, func(i, j int) bool {
		ni, _ := strconv.Atoi(partNames[i])
		nj, _ := strconv.Atoi(partNames[j])
		return ni < nj
	})

	err = permissions.EnsureDirs(filepath.Dir(meta.Path), meta.UID, meta.GID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error ensuring directories: %w", err))
		return
	}

	// Write to a temporary file and rename on success to avoid destroying
	// any pre-existing file at meta.Path if assembly fails midway.
	tmpPath := meta.Path + ".e2b-upload.tmp"

	destFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space available"))
			return
		}

		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error creating destination file: %w", err))

		return
	}

	err = os.Chown(tmpPath, meta.UID, meta.GID)
	if err != nil {
		destFile.Close()
		os.Remove(tmpPath)
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error changing file ownership: %w", err))

		return
	}

	var totalSize int64

	for _, name := range partNames {
		partFile, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			destFile.Close()
			os.Remove(tmpPath)
			jsonError(w, http.StatusInternalServerError, fmt.Errorf("error opening part file %s: %w", name, err))

			return
		}

		n, err := destFile.ReadFrom(partFile)
		partFile.Close()

		if err != nil {
			destFile.Close()
			os.Remove(tmpPath)

			if errors.Is(err, syscall.ENOSPC) {
				jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space available"))
				return
			}

			jsonError(w, http.StatusInternalServerError, fmt.Errorf("error assembling part %s: %w", name, err))

			return
		}

		totalSize += n
	}

	if err := destFile.Close(); err != nil {
		os.Remove(tmpPath)
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error closing destination file: %w", err))

		return
	}

	if err := os.Rename(tmpPath, meta.Path); err != nil {
		os.Remove(tmpPath)
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error finalizing upload: %w", err))

		return
	}

	a.logger.Info().
		Str("uploadId", uploadId).
		Str("path", meta.Path).
		Int64("size", totalSize).
		Msg("Multipart upload completed")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(UploadComplete{
		Path: meta.Path,
		Size: totalSize,
	})
}

func (a *API) DeleteFilesUploadUploadId(w http.ResponseWriter, r *http.Request, uploadId UploadId) {
	defer r.Body.Close()

	if _, err := a.claimUpload(uploadId); err != nil {
		jsonError(w, http.StatusNotFound, err)
		return
	}

	os.RemoveAll(uploadDir(uploadId))

	a.logger.Info().
		Str("uploadId", uploadId).
		Msg("Multipart upload aborted")

	w.WriteHeader(http.StatusNoContent)
}
