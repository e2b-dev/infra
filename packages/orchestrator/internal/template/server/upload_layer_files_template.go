package server

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/paths"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const signedUrlExpiration = time.Minute * 30

func (s *ServerStore) InitLayerFileUpload(ctx context.Context, in *templatemanager.InitLayerFileUploadRequest) (*templatemanager.InitLayerFileUploadResponse, error) {
	ctx, childSpan := tracer.Start(ctx, "template-create")
	defer childSpan.End()

	// default to scope by template ID
	cacheScope := in.GetTemplateID()
	if in.CacheScope != nil {
		cacheScope = in.GetCacheScope()
	}

	path := paths.GetLayerFilesCachePath(cacheScope, in.GetHash())
	obj, err := s.buildStorage.OpenBlob(ctx, path, storage.BuildLayerFileObjectType)
	if err != nil {
		return nil, fmt.Errorf("failed to open layer files cache: %w", err)
	}

	signedUrl, err := s.buildStorage.UploadSignedURL(ctx, path, signedUrlExpiration)
	if err != nil {
		return nil, fmt.Errorf("failed to get signed url: %w", err)
	}

	exists, err := obj.Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check if layer files exists: %w", err)
	}

	return &templatemanager.InitLayerFileUploadResponse{
		Present: exists,
		Url:     &signedUrl,
	}, nil
}
