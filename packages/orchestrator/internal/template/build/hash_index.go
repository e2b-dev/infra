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

func layerMetaFromHash(ctx context.Context, s storage.StorageProvider, finalTemplateID string, hash string) (LayerMetadata, error) {
	obj, err := s.OpenObject(ctx, layerstorage.HashToPath(finalTemplateID, hash))
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error opening object for template metadata: %w", err)
	}

	var buf bytes.Buffer
	_, err = obj.WriteTo(&buf)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error reading template metadata from object: %w", err)
	}

	var templateMetadata LayerMetadata
	err = json.Unmarshal(buf.Bytes(), &templateMetadata)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error unmarshaling template metadata: %w", err)
	}

	if templateMetadata.Template.TemplateID == "" ||
		templateMetadata.Template.BuildID == "" ||
		templateMetadata.Template.KernelVersion == "" ||
		templateMetadata.Template.FirecrackerVersion == "" {
		return LayerMetadata{}, fmt.Errorf("template metadata is missing required fields: %v", templateMetadata)
	}

	return templateMetadata, nil
}

func saveLayerMeta(ctx context.Context, s storage.StorageProvider, finalTemplateID string, hash string, template LayerMetadata) error {
	obj, err := s.OpenObject(ctx, layerstorage.HashToPath(finalTemplateID, hash))
	if err != nil {
		return fmt.Errorf("error creating object for saving UUID: %w", err)
	}

	marshaled, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("error marshalling template metadata: %w", err)
	}

	buf := bytes.NewBuffer(marshaled)
	_, err = obj.ReadFrom(buf)
	if err != nil {
		return fmt.Errorf("error writing UUID to object: %w", err)
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

	return hashKeys(hashingVersion, envdHash, provisionScriptFile, strconv.FormatInt(template.DiskSizeMB, 10), template.FromImage), nil
}

func hashStep(previousHash string, step *templatemanager.TemplateStep) string {
	return hashKeys(previousHash, step.Type, strings.Join(step.Args, " "), utils.Sprintp(step.FilesHash))
}
