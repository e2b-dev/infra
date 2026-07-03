//go:build linux

package chrooted

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/volumes/backend"
)

var ErrVolumeTypeNotFound = errors.New("volume type not found")

// Builder holds one Backend per named volume type and delegates volume
// lifecycle operations to it.
type Builder struct {
	backends map[string]backend.Backend
}

// NewBuilder initialises a Backend for each entry in
// config.PersistentVolumeMounts, all using the same backend type
// (config.VolumeBackendType, e.g. "local", "juicefs", "cephfs").
func NewBuilder(config cfg.Config) (*Builder, error) {
	backends := make(map[string]backend.Backend, len(config.PersistentVolumeMounts))
	for name, root := range config.PersistentVolumeMounts {
		b, err := backend.NewBackend(config.VolumeBackendType, root)
		if err != nil {
			return nil, fmt.Errorf("volume type %q: %w", name, err)
		}
		backends[name] = b
	}
	return &Builder{backends: backends}, nil
}

func (b *Builder) backend(volumeType string) (backend.Backend, error) {
	be, ok := b.backends[volumeType]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrVolumeTypeNotFound, volumeType)
	}
	return be, nil
}

func (b *Builder) Chroot(ctx context.Context, volumeType string, teamID, volumeID uuid.UUID) (*Chrooted, error) {
	fullPath, err := b.BuildVolumePath(volumeType, teamID, volumeID)
	if err != nil {
		return nil, err
	}

	fs, err := Chroot(ctx, fullPath, WithMetadata("volume-id", volumeID.String()))
	if err != nil {
		return nil, err
	}

	return fs, nil
}

func (b *Builder) BuildVolumePath(volumeType string, teamID, volumeID uuid.UUID) (string, error) {
	be, err := b.backend(volumeType)
	if err != nil {
		return "", err
	}
	return be.RootPath(teamID, volumeID), nil
}

// CreateVolume creates the directory tree for the given volume via the backend.
func (b *Builder) CreateVolume(ctx context.Context, volumeType string, teamID, volumeID uuid.UUID) error {
	be, err := b.backend(volumeType)
	if err != nil {
		return err
	}
	return be.CreateVolume(ctx, teamID, volumeID)
}

// DeleteVolume removes the directory tree for the given volume via the backend.
func (b *Builder) DeleteVolume(ctx context.Context, volumeType string, teamID, volumeID uuid.UUID) error {
	be, err := b.backend(volumeType)
	if err != nil {
		return err
	}
	return be.DeleteVolume(ctx, teamID, volumeID)
}
