package api

import (
	"errors"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"net/http"
	"os"
	"os/user"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
)

func (a *API) buildFileReadSigningKey(path string, username string) (string, error) {
	if a.accessToken == nil {
		return "", fmt.Errorf("access token is not set")
	}

	hasher := keys.NewSHA256Hashing()
	signing := fmt.Sprintf("%s:%s:%s", path, username, *a.accessToken)

	return hasher.Hash([]byte(signing)), nil
}

func (a *API) GetFiles(w http.ResponseWriter, r *http.Request, params GetFilesParams) {
	defer r.Body.Close()

	var errorCode int
	var errMsg error

	var path string
	if params.Path != nil {
		path = *params.Path
	}

	// validate signing key
	// todo: we should check if valid token is present in headers, then we can skip this part
	// todo: same logic cane bused for file upload
	if a.accessToken != nil {
		if params.Signing == nil {
			errMsg = fmt.Errorf("missing signing key")
			errorCode = http.StatusUnauthorized

			a.logger.Err(errMsg)
			jsonError(w, errorCode, errMsg)
			return
		}

		hash, err := a.buildFileReadSigningKey(path, params.Username)
		if err != nil {
			errorCode = http.StatusInternalServerError
			errMsg = fmt.Errorf("error building signing key for user '%s' and path '%s'", params.Username, path)

			a.logger.Err(errMsg)
			jsonError(w, errorCode, errMsg)
			return
		}

		if hash != *params.Signing {
			errMsg = fmt.Errorf("bad signing key for user '%s' and path '%s'", params.Username, path)
			errorCode = http.StatusUnauthorized

			a.logger.Err(errMsg)
			jsonError(w, errorCode, errMsg)
			return
		}
	}

	defer func() {
		l := a.logger.
			Err(errMsg).
			Str("method", r.Method+" "+r.URL.Path).
			Str(string(logs.OperationIDKey), logs.AssignOperationID()).
			Str("path", path).
			Str("username", params.Username)

		if errMsg != nil {
			l = l.Int("error_code", errorCode)
		}

		l.Msg("File read")
	}()

	u, err := user.Lookup(params.Username)
	if err != nil {
		errMsg = fmt.Errorf("error looking up user '%s': %w", params.Username, err)
		errorCode = http.StatusUnauthorized
		jsonError(w, errorCode, errMsg)

		return
	}

	resolvedPath, err := permissions.ExpandAndResolve(path, u)
	if err != nil {
		errMsg = fmt.Errorf("error expanding and resolving path '%s': %w", path, err)
		errorCode = http.StatusBadRequest
		jsonError(w, errorCode, errMsg)

		return
	}

	stat, err := os.Stat(resolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			errMsg = fmt.Errorf("path '%s' does not exist", resolvedPath)
			errorCode = http.StatusNotFound
			jsonError(w, errorCode, errMsg)

			return
		}

		errMsg = fmt.Errorf("error checking if path exists '%s': %w", resolvedPath, err)
		errorCode = http.StatusInternalServerError
		jsonError(w, errorCode, errMsg)

		return
	}

	if stat.IsDir() {
		errMsg = fmt.Errorf("path '%s' is a directory", resolvedPath)
		errorCode = http.StatusBadRequest
		jsonError(w, errorCode, errMsg)

		return
	}

	file, err := os.Open(resolvedPath)
	if err != nil {
		errMsg = fmt.Errorf("error opening file '%s': %w", resolvedPath, err)
		errorCode = http.StatusInternalServerError
		jsonError(w, errorCode, errMsg)

		return
	}
	defer file.Close()

	http.ServeContent(w, r, path, time.Now(), file)
}
