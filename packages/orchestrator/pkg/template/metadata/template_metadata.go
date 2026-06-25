//go:build linux

package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/ioutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	CurrentVersion = 2

	DeprecatedVersion = 1

	// FilesystemOnlyVersion is the metadata version that introduced the
	// filesystem_only field. A filesystem-only snapshot must be stamped at least
	// this version so deserialize() (which strips every field from a version <=
	// DeprecatedVersion snapshot) keeps the flag. It is pinned rather than
	// CurrentVersion: CurrentVersion advances as the format grows, and stamping a
	// minimal V1-derived snapshot with a newer version would falsely imply it
	// carries later-version fields.
	FilesystemOnlyVersion = DeprecatedVersion + 1
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata")

// AccessType is a compact representation of block access type for JSON serialization.
type AccessType string

const (
	AccessRead     AccessType = "r"
	AccessWrite    AccessType = "w"
	AccessPrefetch AccessType = "p"
)

// blockToAccessType maps block.AccessType to metadata.AccessType.
var blockToAccessType = map[block.AccessType]AccessType{
	block.Read:     AccessRead,
	block.Write:    AccessWrite,
	block.Prefetch: AccessPrefetch,
}

type Version struct {
	Version any `json:"version"`
}

type Context struct {
	User    string            `json:"user,omitempty"`
	WorkDir *string           `json:"workdir,omitempty"`
	EnvVars map[string]string `json:"env_vars,omitempty"`
}

func (c Context) WithUser(user string) Context {
	c.User = user

	return c
}

type FromTemplate struct {
	Alias   string `json:"alias"`
	BuildID string `json:"build_id"`
}

type Start struct {
	StartCmd string  `json:"start_command"`
	ReadyCmd string  `json:"ready_command"`
	Context  Context `json:"context"`
}

type TemplateMetadata struct {
	BuildID            string `json:"build_id"`
	KernelVersion      string `json:"kernel_version"`
	FirecrackerVersion string `json:"firecracker_version"`
}

// MemoryPrefetchMapping stores block offsets that should be prefetched when starting a sandbox.
// This is used to speed up sandbox starts by proactively fetching blocks that are likely to be needed.
type MemoryPrefetchMapping struct {
	// Indices is an ordered array of block indices to prefetch, preserving the order in which blocks were accessed
	Indices []uint64 `json:"indices"`
	// AccessTypes stores the access type ("r"/"w"/"p") for each block, aligned with Indices
	AccessTypes []AccessType `json:"access_types"`
	// BlockSize is the size of each block in bytes
	BlockSize int64 `json:"block_size"`
}

// AccessTypeFromBlock converts a block.AccessType to metadata.AccessType.
func AccessTypeFromBlock(at block.AccessType) AccessType {
	return blockToAccessType[at]
}

// Count returns the number of blocks to prefetch.
func (p *MemoryPrefetchMapping) Count() int {
	if p == nil {
		return 0
	}

	return len(p.Indices)
}

type Prefetch struct {
	Memory *MemoryPrefetchMapping `json:"memory"`
}

type Template struct {
	Version      uint64           `json:"version"`
	Template     TemplateMetadata `json:"template"`
	Context      Context          `json:"context"`
	Start        *Start           `json:"start,omitempty"`
	FromImage    *string          `json:"from_image,omitempty"`
	FromTemplate *FromTemplate    `json:"from_template,omitempty"`
	Prefetch     *Prefetch        `json:"prefetch,omitempty"`

	// FilesystemOnly marks a snapshot that persists only the filesystem (no
	// memory snapshot); resuming it must cold-boot (reboot) from the rootfs. The
	// zero value (false) is a full memory snapshot, so pre-existing snapshots
	// (no field) are correctly treated as memory without a migration. Stamped at
	// pause time; it is the resume path's source of truth for
	// reboot-vs-memory-resume. Deliberately NOT carried by the
	// With*/SameVersionTemplate copy-constructors — Sandbox.Pause re-stamps it.
	FilesystemOnly bool `json:"filesystem_only,omitempty"`
}

// IsFilesystemOnly reports whether this snapshot persists only the filesystem
// (no memory snapshot), so resuming it must cold-boot (reboot) from the rootfs.
func (t Template) IsFilesystemOnly() bool {
	return t.FilesystemOnly
}

// MarkFilesystemOnly records whether this snapshot persists only the filesystem.
//
// When marking it filesystem-only, the metadata version is upgraded to at least
// FilesystemOnlyVersion if needed: deserialize() strips every field (including
// filesystem_only) from a snapshot whose version is <= DeprecatedVersion, so a
// snapshot taken from a V1 template would otherwise lose the flag on resume and
// be wrongly memory-resumed — and since the filesystem-only pause uploaded no
// memory snapshot, that resume hard-fails. Clearing the flag never changes the
// version.
func (t Template) MarkFilesystemOnly(filesystemOnly bool) Template {
	t.FilesystemOnly = filesystemOnly
	if filesystemOnly && t.Version < FilesystemOnlyVersion {
		t.Version = FilesystemOnlyVersion
	}

	return t
}

func V1TemplateVersion() Template {
	return Template{
		Version: 1,
	}
}

func (t Template) BasedOn(
	ft FromTemplate,
) Template {
	return Template{
		Version:      CurrentVersion,
		Template:     t.Template,
		Context:      t.Context,
		Start:        t.Start,
		FromTemplate: &ft,
		FromImage:    nil,
	}
}

func (t Template) NewVersionTemplate(metadata TemplateMetadata) Template {
	return Template{
		Version:      CurrentVersion,
		Template:     metadata,
		Context:      t.Context,
		Start:        t.Start,
		FromTemplate: t.FromTemplate,
		FromImage:    t.FromImage,
	}
}

func (t Template) SameVersionTemplate(metadata TemplateMetadata) Template {
	return Template{
		Version:      t.Version,
		Template:     metadata,
		Context:      t.Context,
		Start:        t.Start,
		FromTemplate: t.FromTemplate,
		FromImage:    t.FromImage,
	}
}

// WithPrefetch returns a copy of the template with the given prefetch mapping.
func (t Template) WithPrefetch(prefetch *Prefetch) Template {
	return Template{
		Version:      t.Version,
		Template:     t.Template,
		Context:      t.Context,
		Start:        t.Start,
		FromTemplate: t.FromTemplate,
		FromImage:    t.FromImage,
		Prefetch:     prefetch,
	}
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
	return fromTemplate(ctx, s, storage.Paths{
		BuildID: buildID,
	})
}

func fromTemplate(ctx context.Context, s storage.StorageProvider, paths storage.Paths) (Template, error) {
	ctx, span := tracer.Start(ctx, "from template")
	defer span.End()

	obj, err := s.OpenBlob(ctx, paths.Metadata())
	if err != nil {
		return Template{}, fmt.Errorf("error opening object for template metadata: %w", err)
	}

	data, err := storage.GetBlob(ctx, obj)
	if err != nil {
		return Template{}, fmt.Errorf("error reading template metadata from object: %w", err)
	}

	templateMetadata, err := deserialize(bytes.NewReader(data))
	if err != nil {
		return Template{}, err
	}

	return templateMetadata, nil
}

func deserialize(reader io.Reader) (Template, error) {
	// Read all data into bytes first to avoid double stream read
	data, err := io.ReadAll(reader)
	if err != nil {
		return Template{}, fmt.Errorf("error reading template metadata: %w", err)
	}

	var v Version
	err = json.Unmarshal(data, &v)
	if err != nil {
		return Template{}, fmt.Errorf("error unmarshaling template version: %w", err)
	}

	// Handle deprecated version formats gracefully
	// When any type is used, Go will unmarshal any numeric value as a float64
	if version, ok := v.Version.(float64); !ok || version <= float64(DeprecatedVersion) {
		return Template{Version: DeprecatedVersion}, nil
	}

	var templateMetadata Template
	err = json.Unmarshal(data, &templateMetadata)
	if err != nil {
		return Template{}, fmt.Errorf("error unmarshaling template metadata: %w", err)
	}

	return templateMetadata, nil
}

// serialize serializes a template to a reader for uploading.
func serialize(template Template) (io.Reader, error) {
	marshaled, err := json.Marshal(template)
	if err != nil {
		return nil, fmt.Errorf("error serializing template metadata: %w", err)
	}

	buf := bytes.NewBuffer(marshaled)

	return buf, nil
}
