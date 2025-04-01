package storage

import (
	"context"
)

type TemplateLocalBuild struct {
	files *TemplateFiles
}

func (t *TemplateLocalBuild) Remove(ctx context.Context) error {
	return nil
}

func (t *TemplateLocalBuild) Upload(
	ctx context.Context,
	snapfilePath string,
	memfilePath *string,
	rootfsPath *string,
) chan error {
	chanErr := make(chan error, 1)
	close(chanErr)
	return chanErr
}
