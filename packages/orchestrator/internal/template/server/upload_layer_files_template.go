package server

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/paths"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

const signedUrlExpiration = time.Minute * 30

func (s *ServerStore) InitLayerFileUpload(ctx context.Context, in *templatemanager.InitLayerFileUploadRequest) (*templatemanager.InitLayerFileUploadResponse, error) {
	_, childSpan := s.tracer.Start(ctx, "template-create")
	defer childSpan.End()

	// default to scope by template ID
	cacheScope := in.TemplateID
	if in.CacheScope != nil {
		cacheScope = *in.CacheScope
	}

	path := paths.GetLayerFilesCachePath(cacheScope, in.Hash)
	obj, err := s.buildStorage.OpenObject(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to open layer files cache: %w", err)
	}

	signedUrl, err := s.buildStorage.UploadSignedURL(ctx, path, signedUrlExpiration)
	if err != nil {
		return nil, fmt.Errorf("failed to get signed url: %w", err)
	}

	_, err = obj.Size()
	if err != nil {
		return &templatemanager.InitLayerFileUploadResponse{
			Present: false,
			Url:     &signedUrl,
		}, nil
	} else {
		return &templatemanager.InitLayerFileUploadResponse{
			Present: true,
			Url:     &signedUrl,
		}, nil
	}
}
