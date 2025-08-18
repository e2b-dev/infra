package template

import (
	"io"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
)

type Snapfile interface {
	io.Closer

	FirecrackerSnapfile() File
	Metadata() File
	MetadataSerialized() (metadata.TemplateMetadata, error)
}

type StorageSnapfile struct {
	fcSnapfile File
	metadata   File
}

func NewStorageSnapfile(fcSnapfile File, metadata File) *StorageSnapfile {
	return &StorageSnapfile{
		fcSnapfile: fcSnapfile,
		metadata:   metadata,
	}
}

func (s *StorageSnapfile) Close() error {
	var wg errgroup.Group
	wg.Go(func() error {
		return s.fcSnapfile.Close()
	})
	wg.Go(func() error {
		return s.metadata.Close()
	})

	return wg.Wait()
}

func (s *StorageSnapfile) FirecrackerSnapfile() File {
	return s.fcSnapfile
}

func (s *StorageSnapfile) Metadata() File {
	return s.metadata
}

func (s *StorageSnapfile) MetadataSerialized() (metadata.TemplateMetadata, error) {
	return metadata.FromFile(s.metadata.Path())
}

type NoopSnapfile struct{}

func (n *NoopSnapfile) Close() error {
	return nil
}

func (n *NoopSnapfile) FirecrackerSnapfile() File {
	return &NoopFile{}
}

func (n *NoopSnapfile) Metadata() File {
	return &NoopFile{}
}

func (n *NoopSnapfile) MetadataSerialized() (metadata.TemplateMetadata, error) {
	return metadata.TemplateMetadata{}, nil
}

type NoopFile struct{}

func (n *NoopFile) Close() error {
	return nil
}

func (n *NoopFile) Path() string {
	return "/dev/null"
}
