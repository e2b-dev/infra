package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/utils"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const hashingVersion = "v1"

func templateMetaFromHash(ctx context.Context, s storage.StorageProvider, m storage.TemplateFiles, finalTemplateID string, hash string) storage.TemplateFiles {
	newTemplate := storage.TemplateFiles{
		TemplateID:         id.Generate(),
		BuildID:            uuid.New().String(),
		KernelVersion:      m.KernelVersion,
		FirecrackerVersion: m.FirecrackerVersion,
	}

	obj, err := s.OpenObject(ctx, hashToPath(finalTemplateID, hash))
	if err != nil {
		return newTemplate
	}

	var buf bytes.Buffer
	_, err = obj.WriteTo(&buf)
	if err != nil {
		return newTemplate
	}

	var templateMetadata storage.TemplateFiles
	err = json.Unmarshal(buf.Bytes(), &templateMetadata)
	if err != nil {
		zap.L().Error("error unmarshalling template metadata from hash", zap.Error(err))
		return newTemplate
	}

	if templateMetadata.TemplateID == "" ||
		templateMetadata.BuildID == "" ||
		templateMetadata.KernelVersion == "" ||
		templateMetadata.FirecrackerVersion == "" {
		return newTemplate
	}

	return templateMetadata
}

func saveTemplateMeta(ctx context.Context, s storage.StorageProvider, finalTemplateID string, hash string, template storage.TemplateFiles) error {
	obj, err := s.OpenObject(ctx, hashToPath(finalTemplateID, hash))
	if err != nil {
		return fmt.Errorf("error creating object for saving UUID: %w", err)
	}

	marshalled, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("error marshalling template metadata: %w", err)
	}

	buf := bytes.NewBuffer(marshalled)
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
