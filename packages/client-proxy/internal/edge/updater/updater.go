package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/minio/selfupdate"
	"go.uber.org/zap"
	"io"
	"net/http"
	"time"
)

type Updater struct {
	sourceUrl string
	logger    *zap.Logger
}

type UpdaterResponse struct {
	Success bool
	Message string
	Error   error
}

func NewUpdater(sourceUrl string, logger *zap.Logger) *Updater {
	return &Updater{
		sourceUrl: sourceUrl,
		logger:    logger,
	}
}

func (u *Updater) Update(ctx context.Context, expectedHash *string) UpdaterResponse {
	u.logger.Info("checking for update", zap.String("source_url", u.sourceUrl))

	req, err := http.NewRequest(http.MethodGet, u.sourceUrl, nil)
	if err != nil {
		return UpdaterResponse{
			Success: false,
			Message: "Failed to build request to source URL.",
			Error:   err,
		}
	}

	reqCtx, reqCtxCancel := context.WithTimeout(ctx, 10*time.Second)
	defer reqCtxCancel()

	req.WithContext(reqCtx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return UpdaterResponse{
			Success: false,
			Message: "Failed to fetch update from source URL.",
			Error:   err,
		}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return UpdaterResponse{
			Success: false,
			Message: "Failed to fetch update from source URL, status code: " + fmt.Sprintf("%d", resp.StatusCode),
			Error:   err,
		}
	}

	updateBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return UpdaterResponse{
			Success: false,
			Message: "Failed to read update content.",
			Error:   err,
		}
	}

	updateHashRaw := sha256.Sum256(updateBytes)
	updateHash := fmt.Sprintf("%x", updateHashRaw)

	// check if hashes matches, but its optional
	if expectedHash != nil && *expectedHash != updateHash {
		return UpdaterResponse{
			Success: false,
			Message: fmt.Sprintf("Update hash does not match expected (%s) but got (%s).", expectedHash, updateHash),
			Error:   fmt.Errorf("expected %s, got %s", expectedHash, updateHash),
		}
	}

	u.logger.Info("Update is available, processing wit self-update", zap.String("source_url", u.sourceUrl), zap.String("update_hash", updateHash))

	executableBinary := make([]byte, len(updateBytes))
	copy(executableBinary, updateBytes)

	err = selfupdate.Apply(
		bytes.NewReader(executableBinary),
		selfupdate.Options{},
	)

	if err != nil {
		return UpdaterResponse{
			Success: false,
			Message: "Failed to apply update.",
			Error:   err,
		}
	}

	return UpdaterResponse{Success: true}
}
