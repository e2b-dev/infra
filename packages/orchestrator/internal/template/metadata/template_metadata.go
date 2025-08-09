package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	metadataVersion      = "v1"
	templateMetadataFile = "metadata.json"
)

type FromTemplateMetadata struct {
	Alias   string `json:"alias"`
	BuildID string `json:"build_id"`
}

type StartMetadata struct {
	StartCmd string                       `json:"start_command"`
	ReadyCmd string                       `json:"ready_command"`
	Metadata sandboxtools.CommandMetadata `json:"metadata"`
}

type TemplateMetadata struct {
	Version      string                       `json:"version"`
	Template     storage.TemplateFiles        `json:"template"`
	Metadata     sandboxtools.CommandMetadata `json:"metadata"`
	Start        *StartMetadata               `json:"start,omitempty"`
	FromImage    *string                      `json:"from_image,omitempty"`
	FromTemplate *FromTemplateMetadata        `json:"from_template,omitempty"`
}

func templateMetadataPath(buildID string) string {
	return path.Join(buildID, templateMetadataFile)
}

func ReadTemplateMetadata(ctx context.Context, s storage.StorageProvider, buildID string) (TemplateMetadata, error) {
	obj, err := s.OpenObject(ctx, templateMetadataPath(buildID))
	if err != nil {
		return TemplateMetadata{}, fmt.Errorf("open template metadata object: %w", err)
	}

	var buf bytes.Buffer
	_, err = obj.WriteTo(&buf)
	if err != nil {
		return TemplateMetadata{}, fmt.Errorf("read template metadata: %w", err)
	}

	var templateMetadata TemplateMetadata
	err = json.Unmarshal(buf.Bytes(), &templateMetadata)
	if err != nil {
		return TemplateMetadata{}, fmt.Errorf("unmarshal template metadata: %w", err)
	}

	if templateMetadata.Version != metadataVersion {
		return TemplateMetadata{}, fmt.Errorf("template metadata is outdated: %v", templateMetadata)
	}

	return templateMetadata, nil
}

func SaveTemplateMetadata(ctx context.Context, s storage.StorageProvider, buildID string, template TemplateMetadata) error {
	obj, err := s.OpenObject(ctx, templateMetadataPath(buildID))
	if err != nil {
		return fmt.Errorf("create object: %w", err)
	}

	template.Version = metadataVersion
	marshaled, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("marshal template metadata: %w", err)
	}

	buf := bytes.NewBuffer(marshaled)
	_, err = obj.ReadFrom(buf)
	if err != nil {
		return fmt.Errorf("write template metadata: %w", err)
	}

	return nil
}
