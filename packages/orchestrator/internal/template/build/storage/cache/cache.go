package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/paths"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const hashingVersion = "v2"

const minimalCachedTemplateVersion = 2

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache")

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
	ctx, span := tracer.Start(ctx, "get layer_metadata")
	defer span.End()

	data, err := h.indexStorage.GetBlob(ctx, paths.HashToPath(h.cacheScope, hash))
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error reading layer metadata from object: %w", err)
	}

	var layerMetadata LayerMetadata
	err = json.Unmarshal(data, &layerMetadata)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error unmarshaling layer metadata: %w", err)
	}

	if layerMetadata.Template.BuildID == "" {
		return LayerMetadata{}, fmt.Errorf("layer metadata is missing required fields: %v", layerMetadata)
	}

	return layerMetadata, nil
}

func (h *HashIndex) SaveLayerMeta(ctx context.Context, hash string, template LayerMetadata) error {
	ctx, span := tracer.Start(ctx, "save layer_metadata")
	defer span.End()

	marshaled, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("error marshalling layer metadata: %w", err)
	}

	err = h.indexStorage.StoreBlob(ctx, paths.HashToPath(h.cacheScope, hash), bytes.NewReader(marshaled))
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
	ctx, span := tracer.Start(ctx, "is cached")
	defer span.End()

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
