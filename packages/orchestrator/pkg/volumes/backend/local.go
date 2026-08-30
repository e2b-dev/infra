//go:build linux

package backend

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
)

// LocalBackend stores volumes as plain directories on the host filesystem.
// This is the default backend and preserves existing behaviour.
type LocalBackend struct {
	root string
}

func NewLocalBackend(root string) *LocalBackend {
	return &LocalBackend{root: root}
}

func (b *LocalBackend) CreateVolume(_ context.Context, teamID, volumeID uuid.UUID) error {
	if err := os.MkdirAll(b.RootPath(teamID, volumeID), 0o700); err != nil {
		return fmt.Errorf("local: create volume dir: %w", err)
	}

	return nil
}

func (b *LocalBackend) DeleteVolume(_ context.Context, teamID, volumeID uuid.UUID) error {
	if err := os.RemoveAll(b.RootPath(teamID, volumeID)); err != nil {
		return fmt.Errorf("local: delete volume dir: %w", err)
	}

	return nil
}

func (b *LocalBackend) RootPath(teamID, volumeID uuid.UUID) string {
	return volumePath(b.root, teamID, volumeID)
}

func (b *LocalBackend) Healthy(_ context.Context) error {
	if _, err := os.Stat(b.root); err != nil {
		return fmt.Errorf("local: root %q not accessible: %w", b.root, err)
	}

	return nil
}

func (b *LocalBackend) Type() string { return "local" }
