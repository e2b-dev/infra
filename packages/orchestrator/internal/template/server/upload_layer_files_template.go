package server

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

const signedUrlExpiration = time.Minute * 30

func (s *ServerStore) TemplateLayerFilesUpload(ctx context.Context, in *templatemanager.TemplateLayerFilesUploadRequest) (*templatemanager.TemplateLayerFilesUploadResponse, error) {
	_, childSpan := s.tracer.Start(ctx, "template-create")
	defer childSpan.End()

	path := build.GetLayerFilesCachePath(in.TemplateID, in.Hash)
	obj, err := s.storage.OpenObject(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to open layer files cache: %w", err)
	}

	signedUrl, err := s.storage.UploadSignedURL(ctx, path, signedUrlExpiration)
	if err != nil {
		return nil, fmt.Errorf("failed to get signed url: %w", err)
	}

	_, err = obj.Size()
	if err != nil {
		zap.L().Warn("layer files not found", zap.Error(err))

		return &templatemanager.TemplateLayerFilesUploadResponse{
			Present: false,
			Url:     &signedUrl,
		}, nil
	} else {
		return &templatemanager.TemplateLayerFilesUploadResponse{
			Present: true,
			Url:     &signedUrl,
		}, nil
	}
}
