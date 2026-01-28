package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
)

const (
	multipartTempDir = "/tmp/envd-multipart"
	// maxUploadSessions limits concurrent upload sessions to prevent resource exhaustion
	maxUploadSessions = 100
	// maxPartSize limits individual part size to 100MB to prevent DoS
	maxPartSize = 100 * 1024 * 1024
	// uploadSessionTTL is the maximum time an upload session can remain active
	uploadSessionTTL = 1 * time.Hour
	// uploadSessionCleanupInterval is how often to check for expired sessions
	uploadSessionCleanupInterval = 5 * time.Minute
)

// PostFilesUploadInit initializes a multipart upload session
func (a *API) PostFilesUploadInit(w http.ResponseWriter, r *http.Request, params PostFilesUploadInitParams) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Parse the request body
	var body PostFilesUploadInitJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("failed to decode request body")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Validate signing if needed
	err := a.validateSigning(r, params.Signature, params.SignatureExpiration, params.Username, body.Path, SigningWriteOperation)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error during auth validation")
		jsonError(w, http.StatusUnauthorized, err)
		return
	}

	// Resolve username
	username, err := execcontext.ResolveDefaultUsername(params.Username, a.defaults.User)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("no user specified")
		jsonError(w, http.StatusBadRequest, err)
		return
	}

	// Lookup user
	u, err := user.Lookup(username)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Str("username", username).Msg("error looking up user")
		jsonError(w, http.StatusUnauthorized, fmt.Errorf("error looking up user '%s': %w", username, err))
		return
	}

	uid, gid, err := permissions.GetUserIdInts(u)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error getting user ids")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error getting user ids: %w", err))
		return
	}

	// Resolve the file path
	filePath, err := permissions.ExpandAndResolve(body.Path, u, a.defaults.Workdir)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error resolving path")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("error resolving path: %w", err))
		return
	}

	// Create upload ID
	uploadID := uuid.New().String()

	// Create temp directory for this upload
	tempDir := filepath.Join(multipartTempDir, uploadID)
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error creating temp directory")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error creating temp directory: %w", err))
		return
	}

	// Store the session
	session := &MultipartUploadSession{
		UploadID:  uploadID,
		FilePath:  filePath,
		TempDir:   tempDir,
		UID:       uid,
		GID:       gid,
		Parts:     make(map[int]string),
		CreatedAt: time.Now(),
	}

	a.uploadsLock.Lock()
	if len(a.uploads) >= maxUploadSessions {
		a.uploadsLock.Unlock()
		os.RemoveAll(tempDir)
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("maxSessions", maxUploadSessions).Msg("too many concurrent upload sessions")
		jsonError(w, http.StatusTooManyRequests, fmt.Errorf("too many concurrent upload sessions (max %d)", maxUploadSessions))
		return
	}
	a.uploads[uploadID] = session
	a.uploadsLock.Unlock()

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadID).
		Str("filePath", filePath).
		Msg("multipart upload initialized")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(MultipartUploadInit{
		UploadId: uploadID,
	})
}

// PutFilesUploadUploadId uploads a part of a multipart upload
func (a *API) PutFilesUploadUploadId(w http.ResponseWriter, r *http.Request, uploadId string, params PutFilesUploadUploadIdParams) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Validate uploadId is a valid UUID to prevent path traversal
	if _, err := uuid.Parse(uploadId); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("invalid upload ID format")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("invalid upload ID format: must be a valid UUID"))
		return
	}

	// Get the session
	a.uploadsLock.RLock()
	session, exists := a.uploads[uploadId]
	a.uploadsLock.RUnlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("upload session not found: %s", uploadId))
		return
	}

	// Check if session is already being completed/aborted
	if session.completed.Load() {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session is already completing")
		jsonError(w, http.StatusConflict, fmt.Errorf("upload session is already completing or aborted"))
		return
	}

	partNumber := params.Part

	// Check for negative part numbers
	if partNumber < 0 {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("partNumber", partNumber).Msg("invalid part number")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("part number must be non-negative"))
		return
	}

	// Create the part file
	partPath := filepath.Join(session.TempDir, fmt.Sprintf("part_%d", partNumber))

	partFile, err := os.OpenFile(partPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))
			return
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error creating part file")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error creating part file: %w", err))
		return
	}

	// Write the part data using ReadFrom with size limit to prevent DoS
	limitedReader := io.LimitReader(r.Body, maxPartSize+1)
	size, err := partFile.ReadFrom(limitedReader)
	partFile.Close()

	if err != nil {
		os.Remove(partPath)
		if errors.Is(err, syscall.ENOSPC) {
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))
			return
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error writing part data")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error writing part data: %w", err))
		return
	}

	// Check if part exceeded size limit
	if size > maxPartSize {
		os.Remove(partPath)
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int64("size", size).Int64("maxSize", maxPartSize).Msg("part size exceeds limit")
		jsonError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("part size exceeds maximum allowed size of %d bytes", maxPartSize))
		return
	}

	// Record the part (check for duplicates and warn)
	session.mu.Lock()
	if existingPath, exists := session.Parts[partNumber]; exists {
		a.logger.Warn().
			Str(string(logs.OperationIDKey), operationID).
			Str("uploadId", uploadId).
			Int("partNumber", partNumber).
			Str("existingPath", existingPath).
			Msg("overwriting existing part")
	}
	session.Parts[partNumber] = partPath
	session.mu.Unlock()

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Int("partNumber", partNumber).
		Int64("size", size).
		Msg("part uploaded")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(MultipartUploadPart{
		PartNumber: partNumber,
		Size:       size,
	})
}

// PostFilesUploadUploadIdComplete completes a multipart upload and assembles the file
func (a *API) PostFilesUploadUploadIdComplete(w http.ResponseWriter, r *http.Request, uploadId string) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Get and remove the session
	a.uploadsLock.Lock()
	session, exists := a.uploads[uploadId]
	if exists {
		// Mark as completed to prevent new parts from being uploaded
		if !session.completed.CompareAndSwap(false, true) {
			// Already being completed by another request
			a.uploadsLock.Unlock()
			a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session is already completing")
			jsonError(w, http.StatusConflict, fmt.Errorf("upload session is already completing"))
			return
		}
		delete(a.uploads, uploadId)
	}
	a.uploadsLock.Unlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("upload session not found: %s", uploadId))
		return
	}

	// Cleanup temp directory in background (don't block response)
	tempDir := session.TempDir
	logger := a.logger
	defer func() {
		go func() {
			if err := os.RemoveAll(tempDir); err != nil {
				logger.Warn().Err(err).Str("tempDir", tempDir).Msg("failed to cleanup multipart temp directory")
			}
		}()
	}()

	// Track if we need to clean up destination file on error
	destFilePath := session.FilePath
	destFileCreated := false
	assemblySucceeded := false
	defer func() {
		if destFileCreated && !assemblySucceeded {
			if err := os.Remove(destFilePath); err != nil && !os.IsNotExist(err) {
				logger.Warn().Err(err).Str("path", destFilePath).Msg("failed to cleanup partial destination file")
			}
		}
	}()

	// Ensure parent directories exist
	err := permissions.EnsureDirs(filepath.Dir(session.FilePath), session.UID, session.GID)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error ensuring directories")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error ensuring directories: %w", err))
		return
	}

	// Create the destination file
	destFile, err := os.OpenFile(session.FilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))
			return
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error creating destination file")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error creating destination file: %w", err))
		return
	}
	destFileCreated = true

	// Set ownership
	if err := os.Chown(session.FilePath, session.UID, session.GID); err != nil {
		destFile.Close()
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error changing file ownership")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error changing file ownership: %w", err))
		return
	}

	// Get the part numbers and paths in order (copy under lock to avoid race with concurrent uploads)
	session.mu.Lock()
	partNumbers := make([]int, 0, len(session.Parts))
	partPaths := make(map[int]string, len(session.Parts))
	for num, path := range session.Parts {
		partNumbers = append(partNumbers, num)
		partPaths[num] = path
	}
	session.mu.Unlock()
	sort.Ints(partNumbers)

	// Validate that parts are contiguous (0, 1, 2, ..., n-1)
	if len(partNumbers) > 0 {
		for i, partNum := range partNumbers {
			if partNum != i {
				destFile.Close()
				a.logger.Error().
					Str(string(logs.OperationIDKey), operationID).
					Int("expected", i).
					Int("got", partNum).
					Ints("allParts", partNumbers).
					Msg("missing part in upload sequence")
				jsonError(w, http.StatusBadRequest, fmt.Errorf("missing part %d: parts must be contiguous starting from 0", i))
				return
			}
		}
	}

	// Assemble the file using sendfile via io.Copy (which uses copy_file_range on Linux)
	var totalSize int64
	for _, partNum := range partNumbers {
		partPath := partPaths[partNum]
		partFile, err := os.Open(partPath)
		if err != nil {
			destFile.Close()
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Int("partNumber", partNum).Msg("error opening part file")
			jsonError(w, http.StatusInternalServerError, fmt.Errorf("error opening part %d: %w", partNum, err))
			return
		}

		// Use ReadFrom which on Linux uses copy_file_range for zero-copy
		written, err := destFile.ReadFrom(partFile)
		partFile.Close()

		if err != nil {
			destFile.Close()
			if errors.Is(err, syscall.ENOSPC) {
				a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
				jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))
				return
			}
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Int("partNumber", partNum).Msg("error copying part")
			jsonError(w, http.StatusInternalServerError, fmt.Errorf("error copying part %d: %w", partNum, err))
			return
		}

		totalSize += written
	}

	// Close the file before marking success
	if err := destFile.Close(); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error closing destination file")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error closing destination file: %w", err))
		return
	}

	// Mark assembly as successful so we don't clean up the file
	assemblySucceeded = true

	// Note: We skip fsync here for performance. The kernel will flush data to disk
	// eventually. For sandbox use cases, immediate durability is not critical.

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Str("filePath", session.FilePath).
		Int64("totalSize", totalSize).
		Int("numParts", len(partNumbers)).
		Msg("multipart upload completed")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(MultipartUploadComplete{
		Path: session.FilePath,
		Size: totalSize,
	})
}

// DeleteFilesUploadUploadId aborts a multipart upload and cleans up temporary files
func (a *API) DeleteFilesUploadUploadId(w http.ResponseWriter, r *http.Request, uploadId string) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Get and remove the session
	a.uploadsLock.Lock()
	session, exists := a.uploads[uploadId]
	if exists {
		// Mark as completed to prevent new parts from being uploaded
		if !session.completed.CompareAndSwap(false, true) {
			// Already being completed/aborted by another request
			a.uploadsLock.Unlock()
			a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session is already completing")
			jsonError(w, http.StatusConflict, fmt.Errorf("upload session is already completing or aborted"))
			return
		}
		delete(a.uploads, uploadId)
	}
	a.uploadsLock.Unlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("upload session not found: %s", uploadId))
		return
	}

	// Clean up temp directory
	if err := os.RemoveAll(session.TempDir); err != nil {
		a.logger.Warn().Err(err).Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("error cleaning up temp directory")
	}

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Msg("multipart upload aborted")

	w.WriteHeader(http.StatusNoContent)
}
