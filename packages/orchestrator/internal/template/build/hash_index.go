package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layerstorage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/utils"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const hashingVersion = "v1"

type LayerMetadata struct {
	Template storage.TemplateFiles        `json:"template"`
	Metadata sandboxtools.CommandMetadata `json:"metadata"`
}

func layerMetaFromHash(ctx context.Context, s storage.StorageProvider, cacheScope string, hash string) (LayerMetadata, error) {
	obj, err := s.OpenObject(ctx, layerstorage.HashToPath(cacheScope, hash))
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

	if layerMetadata.Template.TemplateID == "" ||
		layerMetadata.Template.BuildID == "" ||
		layerMetadata.Template.KernelVersion == "" ||
		layerMetadata.Template.FirecrackerVersion == "" {
		return LayerMetadata{}, fmt.Errorf("layer metadata is missing required fields: %v", layerMetadata)
	}

	return layerMetadata, nil
}

func saveLayerMeta(ctx context.Context, s storage.StorageProvider, cacheScope string, hash string, template LayerMetadata) error {
	obj, err := s.OpenObject(ctx, layerstorage.HashToPath(cacheScope, hash))
	if err != nil {
		return fmt.Errorf("error creating object for saving UUID: %w", err)
	}

	marshaled, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("error marshalling layer metadata: %w", err)
	}

	buf := bytes.NewBuffer(marshaled)
	_, err = obj.ReadFrom(buf)
	if err != nil {
		return fmt.Errorf("error writing layer metadata to object: %w", err)
	}

	return nil
}

func hashKeys(baseKey string, keys ...string) string {
	sha := sha256.New()
	sha.Write([]byte(baseKey))
	for _, key := range keys {
		sha.Write([]byte(";"))
		sha.Write([]byte(key))
	}
	return fmt.Sprintf("%x", sha.Sum(nil))
}

func hashBase(template config.TemplateConfig) (string, error) {
	envdHash, err := envd.GetEnvdHash()
	if err != nil {
		return "", fmt.Errorf("error getting envd binary hash: %w", err)
	}

	var baseSource string
	if template.FromTemplate != nil {
		// When building from template, use the base template metadata
		baseSource = fmt.Sprintf("template:%s", template.FromTemplate.GetBuildID())
	} else {
		// When building from image, use the image name
		baseSource = template.FromImage
	}

	return hashKeys(hashingVersion, envdHash, provisionScriptFile, strconv.FormatInt(template.DiskSizeMB, 10), baseSource), nil
}

func hashStep(previousHash string, step *templatemanager.TemplateStep) string {
	return hashKeys(previousHash, step.Type, strings.Join(step.Args, " "), utils.Sprintp(step.FilesHash))
}
