package artefacts_registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type RegistryProvider string

const (
	GCPStorageProvider   RegistryProvider = "GCP_ARTIFACTS"
	AWSStorageProvider   RegistryProvider = "AWS_ECR"
	LocalStorageProvider RegistryProvider = "Local"

	DefaultRegistryProvider RegistryProvider = GCPStorageProvider

	storageProviderEnv = "ARTEFACTS_REGISTRY_PROVIDER"
)

var (
	ErrImageNotExists = errors.New("image does not exist")
)

type ArtefactsRegistry interface {
	GetUrl(ctx context.Context, templateId string, buildId string) (string, error)
	GetImage(ctx context.Context, templateId string, buildId string, platform v1.Platform) (v1.Image, error)
	Delete(ctx context.Context, templateId string, buildId string) error
}

func GetArtefactsRegistryProvider() (ArtefactsRegistry, error) {
	var provider = RegistryProvider(env.GetEnv(storageProviderEnv, string(DefaultRegistryProvider)))

	setupCtx, setupCtxCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer setupCtxCancel()

	switch provider {
	case AWSStorageProvider:
		return NewAWSArtefactsRegistry(setupCtx)
	case GCPStorageProvider:
		return NewGCPArtefactsRegistry(setupCtx)
	case LocalStorageProvider:
		return NewLocalArtefactsRegistry()
	}

	return nil, fmt.Errorf("unknown artefacts registry provider: %s", provider)
}
