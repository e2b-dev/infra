package api

import (
	"encoding/json"
	"errors"
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

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

var ErrNoDiskSpace = fmt.Errorf("not enough disk space available")

func processFile(r *http.Request, path string, part io.Reader, uid, gid int, logger zerolog.Logger) (int, error) {
	logger.Debug().
		Str("path", path).
		Msg("File processing")

	err := permissions.EnsureDirs(filepath.Dir(path), uid, gid)
	if err != nil {
		err := fmt.Errorf("error ensuring directories: %w", err)

		return http.StatusInternalServerError, err
	}

	canBePreChowned := false
	stat, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		errMsg := fmt.Errorf("error getting file info: %w", err)

		return http.StatusInternalServerError, errMsg
	} else if err == nil {
		if stat.IsDir() {
			err := fmt.Errorf("path is a directory: %s", path)

			return http.StatusBadRequest, err
		}
		canBePreChowned = true
	}

	hasBeenChowned := false
	if canBePreChowned {
		err = os.Chown(path, uid, gid)
		if err != nil {
			if !os.IsNotExist(err) {
				err = fmt.Errorf("error changing file ownership: %w", err)

				return http.StatusInternalServerError, err
			}
		} else {
			hasBeenChowned = true
		}
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			err = fmt.Errorf("not enough inodes available: %w", err)

			return http.StatusInsufficientStorage, err
		}

		err := fmt.Errorf("error opening file: %w", err)

		return http.StatusInternalServerError, err
	}

	defer file.Close()

	if !hasBeenChowned {
		err = os.Chown(path, uid, gid)
		if err != nil {
			err := fmt.Errorf("error changing file ownership: %w", err)

			return http.StatusInternalServerError, err
		}
	}

	_, err = file.ReadFrom(part)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			err = ErrNoDiskSpace
			if r.ContentLength > 0 {
				err = fmt.Errorf("attempted to write %d bytes: %w", r.ContentLength, err)
			}

			return http.StatusInsufficientStorage, err
		}

		err = fmt.Errorf("error writing file: %w", err)

		return http.StatusInternalServerError, err
	}

	return http.StatusNoContent, nil
}

func resolvePath(part *multipart.Part, paths *UploadSuccess, u *user.User, defaultPath *string, params PostFilesParams) (string, error) {
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

	filePath, err := permissions.ExpandAndResolve(pathToResolve, u, defaultPath)
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

	username, err := execcontext.ResolveDefaultUsername(params.Username, a.defaults.User)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("no user specified")
		jsonError(w, http.StatusBadRequest, err)

		return
	}

	defer func() {
		l := a.logger.
			Err(errMsg).
			Str("method", r.Method+" "+r.URL.Path).
			Str(string(logs.OperationIDKey), operationID).
			Str("path", path).
			Str("username", username)

		if errMsg != nil {
			l = l.Int("error_code", errorCode)
		}

		l.Msg("File write")
	}()

	// Handle gzip-encoded request body
	body, err := getDecompressedBody(r)
	if err != nil {
		errMsg = fmt.Errorf("error decompressing request body: %w", err)
		errorCode = http.StatusBadRequest
		jsonError(w, errorCode, errMsg)

		return
	}
	defer body.Close()
	r.Body = body

	f, err := r.MultipartReader()
	if err != nil {
		errMsg = fmt.Errorf("error parsing multipart form: %w", err)
		errorCode = http.StatusInternalServerError
		jsonError(w, errorCode, errMsg)

		return
	}

	u, err := user.Lookup(username)
	if err != nil {
		errMsg = fmt.Errorf("error looking up user '%s': %w", username, err)
		errorCode = http.StatusUnauthorized

		jsonError(w, errorCode, errMsg)

		return
	}

	uid, gid, err := permissions.GetUserIdInts(u)
	if err != nil {
		errMsg = fmt.Errorf("error getting user ids: %w", err)

		jsonError(w, http.StatusInternalServerError, errMsg)

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
			filePath, err := resolvePath(part, &paths, u, a.defaults.Workdir, params)
			if err != nil {
				errorCode = http.StatusBadRequest
				errMsg = err
				jsonError(w, errorCode, errMsg)

				return
			}

			logger := a.logger.
				With().
				Str(string(logs.OperationIDKey), operationID).
				Str("event_type", "file_processing").
				Logger()
			status, err := processFile(r, filePath, part, uid, gid, logger)
			if err != nil {
				errorCode = status
				errMsg = err
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
