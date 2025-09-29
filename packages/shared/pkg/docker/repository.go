package docker

import (
	"context"
	"fmt"
	"time"

	containerregistry "github.com/google/go-containerregistry/pkg/v1"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type RemoteRepositoryProvider string

const (
	GCPStorageProvider   RemoteRepositoryProvider = "GCP_REMOTE_REPOSITORY"
	AWSStorageProvider   RemoteRepositoryProvider = "AWS_ECR"
	LocalStorageProvider RemoteRepositoryProvider = "Local"

	DefaultRegistryProvider RemoteRepositoryProvider = GCPStorageProvider

	storageProviderEnv         = "DOCKER_REMOTE_REPOSITORY_PROVIDER"
	storageRemoteRepositoryURL = "DOCKER_REMOTE_REPOSITORY_URL"
)

type RemoteRepository interface {
	GetImage(ctx context.Context, tag string, platform containerregistry.Platform) (containerregistry.Image, error)
}

func GetRemoteRepository(ctx context.Context) (RemoteRepository, error) {
	provider := RemoteRepositoryProvider(env.GetEnv(storageProviderEnv, string(DefaultRegistryProvider)))

	dockerRemoteRepositoryURL := env.GetEnv(storageRemoteRepositoryURL, "")
	if dockerRemoteRepositoryURL == "" {
		return NewNoopRemoteRepository(), nil
	}

	setupCtx, setupCtxCancel := context.WithTimeout(ctx, 10*time.Second)
	defer setupCtxCancel()

	switch provider {
	case AWSStorageProvider:
		return NewAWSRemoteRepository(setupCtx, dockerRemoteRepositoryURL)
	case GCPStorageProvider:
		return NewGCPRemoteRepository(setupCtx, dockerRemoteRepositoryURL)
	case LocalStorageProvider:
		return NewNoopRemoteRepository(), nil
	}

	return nil, fmt.Errorf("unknown docker remote repository provider: %s", provider)
}
