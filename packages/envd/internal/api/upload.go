package api

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

func freeDiskSpace(path string) (free uint64, err error) {
	var stat syscall.Statfs_t

	err = syscall.Statfs(path, &stat)
	if err != nil {
		return 0, fmt.Errorf("error getting free disk space: %w", err)
	}

	// Available blocks * size per block = available space in bytes
	freeSpace := stat.Bavail * uint64(stat.Bsize)

	return freeSpace, nil
}

func processFile(r *http.Request, path string, part *multipart.Part, user *user.User, logger zerolog.Logger) (int, error) {
	logger.Debug().
		Str("path", path).
		Msg("File processing")

	uid, gid, err := permissions.GetUserIds(user)
	if err != nil {
		errMsg := fmt.Errorf("error getting user ids: %w", err)

		return http.StatusInternalServerError, errMsg
	}

	err = permissions.EnsureDirs(filepath.Dir(path), int(uid), int(gid))
	if err != nil {
		errMsg := fmt.Errorf("error ensuring directories: %w", err)

		return http.StatusInternalServerError, errMsg
	}

	freeSpace, err := freeDiskSpace(filepath.Dir(path))
	if err != nil {
		errMsg := fmt.Errorf("error checking free disk space: %w", err)

		return http.StatusInternalServerError, errMsg
	}

	// Sometimes the size can be unknown resulting in ContentLength being -1 or 0.
	// We are still comparing these values — this condition will just always evaluate false for them.
	if r.ContentLength > 0 && freeSpace < uint64(r.ContentLength) {
		errMsg := fmt.Errorf("not enough disk space on '%s': %d bytes required, %d bytes free", filepath.Dir(path), r.ContentLength, freeSpace)

		return http.StatusInsufficientStorage, errMsg
	}

	stat, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		errMsg := fmt.Errorf("error getting file info: %w", err)

		return http.StatusInternalServerError, errMsg
	}

	if err == nil {
		if stat.IsDir() {
			errMsg := fmt.Errorf("path is a directory: %s", path)

			return http.StatusBadRequest, errMsg
		}
	}

	file, err := os.Create(path)
	if err != nil {
		errMsg := fmt.Errorf("error creating file: %w", err)

		return http.StatusInternalServerError, errMsg
	}

	defer file.Close()

	err = os.Chown(path, int(uid), int(gid))
	if err != nil {
		errMsg := fmt.Errorf("error changing file ownership: %w", err)

		return http.StatusInternalServerError, errMsg
	}

	_, readErr := file.ReadFrom(part)
	if readErr != nil {
		errMsg := fmt.Errorf("error reading file: %w", readErr)

		return http.StatusInternalServerError, errMsg
	}

	return http.StatusNoContent, nil
}

func resolvePath(part *multipart.Part, paths *UploadSuccess, u *user.User, params PostFilesParams) (string, error) {
	var pathToResolve string

	if params.Path != nil {
		pathToResolve = *params.Path
	} else {
		var err error
		customPart := utils.NewCustomPart(part)
		pathToResolve, err = customPart.FileNameWithPath()
		if err != nil {
			return "", fmt.Errorf("error getting multipart custom part file name: %w", err)
		}
	}

	filePath, err := permissions.ExpandAndResolve(pathToResolve, u)
	if err != nil {
		return "", fmt.Errorf("error resolving path: %w", err)
	}

	for _, entry := range *paths {
		if entry.Path == filePath {
			var alreadyUploaded []string
			for _, uploadedFile := range *paths {
				if uploadedFile.Path != filePath {
					alreadyUploaded = append(alreadyUploaded, uploadedFile.Path)
				}
			}

			errMsg := fmt.Errorf("you cannot upload multiple files to the same path '%s' in one upload request, only the first specified file was uploaded", filePath)

			if len(alreadyUploaded) > 1 {
				errMsg = fmt.Errorf("%w, also the following files were uploaded: %v", errMsg, strings.Join(alreadyUploaded, ", "))
			}

			return "", errMsg
		}
	}

	return filePath, nil
}

func (a *API) PostFiles(w http.ResponseWriter, r *http.Request, params PostFilesParams) {
	defer r.Body.Close()

	var errorCode int
	var errMsg error

	var path string
	if params.Path != nil {
		path = *params.Path
	}

	operationID := logs.AssignOperationID()

	// signing authorization if needed
	err := a.validateSigning(r, params.Signature, params.SignatureExpiration, params.Username, path, SigningWriteOperation)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error during auth validation")
		jsonError(w, http.StatusUnauthorized, err)
		return
	}

	defer func() {
		l := a.logger.
			Err(errMsg).
			Str("method", r.Method+" "+r.URL.Path).
			Str(string(logs.OperationIDKey), operationID).
			Str("path", path).
			Str("username", params.Username)

		if errMsg != nil {
			l = l.Int("error_code", errorCode)
		}

		l.Msg("File write")
	}()

	f, err := r.MultipartReader()
	if err != nil {
		errMsg = fmt.Errorf("error parsing multipart form: %w", err)
		errorCode = http.StatusInternalServerError
		jsonError(w, errorCode, errMsg)

		return
	}

	u, err := user.Lookup(params.Username)
	if err != nil {
		errMsg = fmt.Errorf("error looking up user '%s': %w", params.Username, err)
		errorCode = http.StatusUnauthorized

		jsonError(w, errorCode, errMsg)

		return
	}

	paths := UploadSuccess{}

	for {
		part, partErr := f.NextPart()

		if partErr == io.EOF {
			// We're done reading the parts.
			break
		} else if partErr != nil {
			errMsg = fmt.Errorf("error reading form: %w", partErr)
			errorCode = http.StatusInternalServerError
			jsonError(w, errorCode, errMsg)

			break
		}

		if part.FormName() == "file" {
			filePath, err := resolvePath(part, &paths, u, params)
			if err != nil {
				errorCode = http.StatusBadRequest
				errMsg = err
				jsonError(w, errorCode, errMsg)

				return
			}

			status, processErr := processFile(r, filePath, part, u, a.logger.With().Str(string(logs.OperationIDKey), operationID).Str("event_type", "file_processing").Logger())
			if processErr != nil {
				errorCode = status
				errMsg = processErr
				jsonError(w, errorCode, errMsg)

				return
			}

			paths = append(paths, EntryInfo{
				Path: filePath,
				Name: filepath.Base(filePath),
				Type: File,
			})
		}

		part.Close()
	}

	data, err := json.Marshal(paths)
	if err != nil {
		errMsg = fmt.Errorf("error marshaling response: %w", err)
		errorCode = http.StatusInternalServerError
		jsonError(w, errorCode, errMsg)

		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
