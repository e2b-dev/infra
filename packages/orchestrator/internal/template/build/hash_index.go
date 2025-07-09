package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/envd"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func getBaseHash(ctx context.Context, template *config.TemplateConfig) (string, error) {
	envdHash, err := envd.GetEnvdHash(ctx)
	if err != nil {
		return "", fmt.Errorf("error getting envd binary hash: %w", err)
	}

	baseSHA := sha256.New()
	baseSHA.Write([]byte(envdHash))
	baseSHA.Write([]byte(provisionScriptFile))
	baseSHA.Write([]byte(strconv.FormatInt(template.DiskSizeMB, 10)))

	return fmt.Sprintf("%x", baseSHA.Sum(nil)), nil
}

func getTemplateFromHash(ctx context.Context, s storage.StorageProvider, templateID string, baseHash string, hash string) config.TemplateMetadata {
	obj, err := s.OpenObject(ctx, uuidToHashPath(templateID, baseHash, hash))
	if err != nil {
		return config.TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.New().String(),
		}
	}

	var buf bytes.Buffer
	_, err = obj.WriteTo(&buf)
	if err != nil {
		return config.TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.New().String(),
		}
	}

	var templateMetadata config.TemplateMetadata
	err = json.Unmarshal(buf.Bytes(), &templateMetadata)
	if err != nil {
		zap.L().Error("error unmarshalling template metadata from hash", zap.Error(err))
		return config.TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.New().String(),
		}
	}

	return templateMetadata
}

func saveTemplateToHash(ctx context.Context, s storage.StorageProvider, templateID string, baseHash, hash string, template config.TemplateMetadata) error {
	obj, err := s.OpenObject(ctx, uuidToHashPath(templateID, baseHash, hash))
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

func uuidToHashPath(templateID, baseHash, hash string) string {
	reSHA := sha256.New()
	reSHA.Write([]byte(baseHash))
	reSHA.Write([]byte(hash))
	reHash := fmt.Sprintf("%x", reSHA.Sum(nil))
	return fmt.Sprintf("builder/cache/%s/index/%s", templateID, reHash)
}
