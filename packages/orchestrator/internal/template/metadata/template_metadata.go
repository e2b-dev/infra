package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/ioutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	CurrentVersion = 2
)

type CommandMetadata struct {
	User    string            `json:"user,omitempty"`
	WorkDir *string           `json:"workdir,omitempty"`
	EnvVars map[string]string `json:"env_vars,omitempty"`
}

type FromTemplateMetadata struct {
	Alias   string `json:"alias"`
	BuildID string `json:"build_id"`
}

type StartMetadata struct {
	StartCmd string          `json:"start_command"`
	ReadyCmd string          `json:"ready_command"`
	Metadata CommandMetadata `json:"metadata"`
}

type TemplateMetadata struct {
	Version      uint64                `json:"version"`
	Template     storage.TemplateFiles `json:"template"`
	Metadata     CommandMetadata       `json:"metadata"`
	Start        *StartMetadata        `json:"start,omitempty"`
	FromImage    *string               `json:"from_image,omitempty"`
	FromTemplate *FromTemplateMetadata `json:"from_template,omitempty"`
}

func (tm TemplateMetadata) NextTemplate(
	fromTemplate FromTemplateMetadata,
) TemplateMetadata {
	return TemplateMetadata{
		Version:      CurrentVersion,
		Template:     tm.Template,
		Metadata:     tm.Metadata,
		Start:        tm.Start,
		FromTemplate: &fromTemplate,
		FromImage:    nil,
	}
}

func (tm TemplateMetadata) UpdateVersion() TemplateMetadata {
	tm.Version = CurrentVersion
	return tm
}

func (tm TemplateMetadata) ToFile(path string) error {
	mr, err := serialize(tm)
	if err != nil {
		return err
	}

	err = ioutils.WriteToFileFromReader(path, mr)
	if err != nil {
		return fmt.Errorf("failed to write metadata to file: %w", err)
	}

	return nil
}

func FromFile(path string) (TemplateMetadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return TemplateMetadata{}, fmt.Errorf("failed to open metadata file: %w", err)
	}
	defer f.Close()

	templateMetadata, err := deserialize(f)
	if err != nil {
		return TemplateMetadata{}, fmt.Errorf("failed to deserialize metadata: %w", err)
	}

	return templateMetadata, nil
}

func FromBuildID(ctx context.Context, s storage.StorageProvider, buildID string) (TemplateMetadata, error) {
	return fromTemplate(ctx, s, storage.TemplateFiles{
		BuildID: buildID,
	})
}

func fromTemplate(ctx context.Context, s storage.StorageProvider, files storage.TemplateFiles) (TemplateMetadata, error) {
	obj, err := s.OpenObject(ctx, files.StorageMetadataPath())
	if err != nil {
		return TemplateMetadata{}, fmt.Errorf("error opening object for template metadata: %w", err)
	}

	var buf bytes.Buffer
	_, err = obj.WriteTo(&buf)
	if err != nil {
		return TemplateMetadata{}, fmt.Errorf("error reading template metadata from object: %w", err)
	}

	templateMetadata, err := deserialize(&buf)
	if err != nil {
		return TemplateMetadata{}, err
	}

	return templateMetadata, nil
}

func deserialize(reader io.Reader) (TemplateMetadata, error) {
	decoder := json.NewDecoder(reader)

	var templateMetadata TemplateMetadata
	err := decoder.Decode(&templateMetadata)
	if err != nil {
		return TemplateMetadata{}, fmt.Errorf("error unmarshaling template metadata: %w", err)
	}

	return templateMetadata, nil
}

func serialize(template TemplateMetadata) (io.Reader, error) {
	marshaled, err := json.Marshal(template)
	if err != nil {
		return nil, fmt.Errorf("error serializing template metadata: %w", err)
	}

	buf := bytes.NewBuffer(marshaled)

	return buf, nil
}
