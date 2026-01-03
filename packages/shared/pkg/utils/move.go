package utils

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// RenameOrDeleteFile tries to rename a file but will not replace the target if it already exists.
// If the file already exists, the file will be deleted. The old file will always be deleted.
func RenameOrDeleteFile(ctx context.Context, oldPath, newPath string) error {
	return removeOrDeleteFile(ctx, oldPath, newPath, &osFns{})
}

func removeOrDeleteFile(ctx context.Context, oldPath, newPath string, os fileOps) error {
	defer func() {
		if err := os.Remove(oldPath); err != nil {
			logger.L().Warn(ctx, "failed to remove existing file",
				zap.Error(err),
				zap.String("path", oldPath),
			)
		}
	}()

	if err := os.Link(oldPath, newPath); err != nil {
		return fmt.Errorf("failed to create hard link: %w", err)
	}

	return nil
}

type osFns struct{}

func (o osFns) Link(oldPath, newPath string) error {
	return os.Link(oldPath, newPath)
}

func (o osFns) Remove(path string) error {
	return os.Remove(path)
}

var _ fileOps = (*osFns)(nil)

type fileOps interface {
	Link(oldPath, newPath string) error
	Remove(path string) error
}
