package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/ioutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	CurrentVersion = 2
)

type Command struct {
	User    string            `json:"user,omitempty"`
	WorkDir *string           `json:"workdir,omitempty"`
	EnvVars map[string]string `json:"env_vars,omitempty"`
}

type FromTemplate struct {
	Alias   string `json:"alias"`
	BuildID string `json:"build_id"`
}

type Start struct {
	StartCmd string  `json:"start_command"`
	ReadyCmd string  `json:"ready_command"`
	Metadata Command `json:"metadata"`
}

type Template struct {
	Version      uint64                `json:"version"`
	Template     storage.TemplateFiles `json:"template"`
	Metadata     Command               `json:"metadata"`
	Start        *Start                `json:"start,omitempty"`
	FromImage    *string               `json:"from_image,omitempty"`
	FromTemplate *FromTemplate         `json:"from_template,omitempty"`
}

func (t Template) NextTemplate(
	fromTemplate FromTemplate,
) Template {
	return Template{
		Version:      CurrentVersion,
		Template:     t.Template,
		Metadata:     t.Metadata,
		Start:        t.Start,
		FromTemplate: &fromTemplate,
		FromImage:    nil,
	}
}

func (t Template) UpdateVersion() Template {
	t.Version = CurrentVersion
	return t
}

func (t Template) ToFile(path string) error {
	mr, err := serialize(t)
	if err != nil {
		return err
	}

	err = ioutils.WriteToFileFromReader(path, mr)
	if err != nil {
		return fmt.Errorf("failed to write metadata to file: %w", err)
	}

	return nil
}

func FromFile(path string) (Template, error) {
	f, err := os.Open(path)
	if err != nil {
		return Template{}, fmt.Errorf("failed to open metadata file: %w", err)
	}
	defer f.Close()

	templateMetadata, err := deserialize(f)
	if err != nil {
		return Template{}, fmt.Errorf("failed to deserialize metadata: %w", err)
	}

	return templateMetadata, nil
}

func FromBuildID(ctx context.Context, s storage.StorageProvider, buildID string) (Template, error) {
	return fromTemplate(ctx, s, storage.TemplateFiles{
		BuildID: buildID,
	})
}

func fromTemplate(ctx context.Context, s storage.StorageProvider, files storage.TemplateFiles) (Template, error) {
	obj, err := s.OpenObject(ctx, files.StorageMetadataPath())
	if err != nil {
		return Template{}, fmt.Errorf("error opening object for template metadata: %w", err)
	}

	var buf bytes.Buffer
	_, err = obj.WriteTo(&buf)
	if err != nil {
		return Template{}, fmt.Errorf("error reading template metadata from object: %w", err)
	}

	templateMetadata, err := deserialize(&buf)
	if err != nil {
		return Template{}, err
	}

	return templateMetadata, nil
}

func deserialize(reader io.Reader) (Template, error) {
	start := time.Now()
	decoder := json.NewDecoder(reader)

	var templateMetadata Template
	err := decoder.Decode(&templateMetadata)
	if err != nil {
		return Template{}, fmt.Errorf("error unmarshaling template metadata: %w", err)
	}

	zap.L().Error("template metadata deserialized",
		zap.String("duration", time.Since(start).String()),
		zap.Uint64("version", templateMetadata.Version),
		zap.String("build_id", templateMetadata.Template.BuildID),
	)
	return templateMetadata, nil
}

func serialize(template Template) (io.Reader, error) {
	start := time.Now()
	marshaled, err := json.Marshal(template)
	if err != nil {
		return nil, fmt.Errorf("error serializing template metadata: %w", err)
	}

	buf := bytes.NewBuffer(marshaled)

	zap.L().Debug("template metadata serialized",
		zap.String("duration", time.Since(start).String()),
		zap.Uint64("version", template.Version),
		zap.String("build_id", template.Template.BuildID),
	)
	return buf, nil
}
