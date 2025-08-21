package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/paths"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const hashingVersion = "v1"

const minimalCachedTemplateVersion = 2

type Template struct {
	BuildID string `json:"build_id"`
}

type LayerMetadata struct {
	Template Template `json:"template"`
}

type Index interface {
	LayerMetaFromHash(ctx context.Context, hash string) (LayerMetadata, error)
	SaveLayerMeta(ctx context.Context, hash string, template LayerMetadata) error
	Cached(ctx context.Context, buildID string) (metadata.Template, error)
	Version() string
}

type HashIndex struct {
	cacheScope      string
	indexStorage    storage.StorageProvider
	templateStorage storage.StorageProvider
	version         string
}

func NewHashIndex(
	cacheScope string,
	indexStorage storage.StorageProvider,
	templateStorage storage.StorageProvider,
) *HashIndex {
	return &HashIndex{
		cacheScope:      cacheScope,
		indexStorage:    indexStorage,
		templateStorage: templateStorage,
		version:         hashingVersion,
	}
}

func (h *HashIndex) Version() string {
	return h.version
}

func (h *HashIndex) LayerMetaFromHash(ctx context.Context, hash string) (LayerMetadata, error) {
	obj, err := h.indexStorage.OpenObject(ctx, paths.HashToPath(h.cacheScope, hash))
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error opening object for layer metadata: %w", err)
	}

	var buf bytes.Buffer
	_, err = obj.WriteTo(&buf)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error reading layer metadata from object: %w", err)
	}

	var layerMetadata LayerMetadata
	err = json.Unmarshal(buf.Bytes(), &layerMetadata)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error unmarshaling layer metadata: %w", err)
	}

	if layerMetadata.Template.BuildID == "" {
		return LayerMetadata{}, fmt.Errorf("layer metadata is missing required fields: %v", layerMetadata)
	}

	return layerMetadata, nil
}

func (h *HashIndex) SaveLayerMeta(ctx context.Context, hash string, template LayerMetadata) error {
	obj, err := h.indexStorage.OpenObject(ctx, paths.HashToPath(h.cacheScope, hash))
	if err != nil {
		return fmt.Errorf("error creating object for saving UUID: %w", err)
	}

	marshaled, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("error marshalling layer metadata: %w", err)
	}

	_, err = obj.Write(marshaled)
	if err != nil {
		return fmt.Errorf("error writing layer metadata to object: %w", err)
	}

	return nil
}

func HashKeys(baseKey string, keys ...string) string {
	sha := sha256.New()
	sha.Write([]byte(baseKey))
	for _, key := range keys {
		sha.Write([]byte(";"))
		sha.Write([]byte(key))
	}
	return fmt.Sprintf("%x", sha.Sum(nil))
}

func (h *HashIndex) Cached(
	ctx context.Context,
	buildID string,
) (metadata.Template, error) {
	tmpl, err := metadata.FromBuildID(ctx, h.templateStorage, buildID)
	if err != nil {
		// If the metadata does not exist, the layer is not cached
		return metadata.Template{}, fmt.Errorf("error reading template metadata: %w", err)
	}

	if tmpl.Version < minimalCachedTemplateVersion || tmpl.Version <= metadata.DeprecatedVersion {
		return metadata.Template{}, fmt.Errorf("outdated template metadata: expected %d, got %d", metadata.CurrentVersion, tmpl.Version)
	}

	// If the metadata exists, the layer is cached
	return tmpl, nil
}
