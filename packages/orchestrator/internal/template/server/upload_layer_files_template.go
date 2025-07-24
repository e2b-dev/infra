package server

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layerstorage"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

const signedUrlExpiration = time.Minute * 30

func (s *ServerStore) InitLayerFileUpload(ctx context.Context, in *templatemanager.InitLayerFileUploadRequest) (*templatemanager.InitLayerFileUploadResponse, error) {
	_, childSpan := s.tracer.Start(ctx, "template-create")
	defer childSpan.End()

	teamID := in.TeamID
	if teamID == "" {
		// For backward compatibility, if teamID is not provided, use the TemplateID as teamID
		// The TeamID is used now only for namespacing the caches
		teamID = in.TemplateID
	}

	path := layerstorage.GetLayerFilesCachePath(teamID, in.Hash)
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
