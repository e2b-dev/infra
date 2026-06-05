//go:build linux

package template

import (
	"context"
	"errors"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// LayeredTemplate composes multiple snapshot layers (L0, L1, L2) into a
// single Template. Each layer may come from a different source (local
// NVMe cache, GCS, peer orchestrator) and is resolved independently.
type LayeredTemplate struct {
	l0     Template // infrastructure layer (may be nil for non-layered)
	l1     Template // runtime layer (may be nil for non-layered)
	l2     Template // instance layer (always present — the "current" template)
	meta   metadata.Template
	files  storage.CachePaths
	closed bool
}

// NewLayeredTemplate creates a layered template. At least l2 must be
// non-nil. l0 and l1 are optional — when nil, the template degrades
// gracefully to the single-layer behavior.
func NewLayeredTemplate(l0, l1, l2 Template, meta metadata.Template, files storage.CachePaths) (*LayeredTemplate, error) {
	if l2 == nil {
		return nil, errors.New("layered template requires at least an L2 template")
	}
	return &LayeredTemplate{
		l0:    l0,
		l1:    l1,
		l2:    l2,
		meta:  meta,
		files: files,
	}, nil
}

func (t *LayeredTemplate) Files() storage.CachePaths {
	return t.files
}

func (t *LayeredTemplate) Memfile(ctx context.Context) (block.ReadonlyDevice, error) {
	return t.l2.Memfile(ctx)
}

func (t *LayeredTemplate) Rootfs() (block.ReadonlyDevice, error) {
	return t.l2.Rootfs()
}

func (t *LayeredTemplate) Snapfile() (File, error) {
	return t.l2.Snapfile()
}

func (t *LayeredTemplate) Metadata() (metadata.Template, error) {
	return t.meta, nil
}

func (t *LayeredTemplate) UpdateMetadata(meta metadata.Template) error {
	t.meta = meta
	return t.l2.UpdateMetadata(meta)
}

func (t *LayeredTemplate) Close(ctx context.Context) error {
	if t.closed {
		return nil
	}
	t.closed = true

	var errs []error
	for _, tmpl := range []Template{t.l2, t.l1, t.l0} {
		if tmpl != nil {
			if err := tmpl.Close(ctx); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (t *LayeredTemplate) Layer() Layer {
	if t.l0 != nil && t.l1 != nil {
		return Layer2
	}
	if t.l0 != nil {
		return Layer1
	}
	return Layer0
}

func (t *LayeredTemplate) ParentTemplate() Template {
	if t.l1 != nil {
		return t.l1
	}
	return t.l0
}

// L0 returns the infrastructure layer template, or nil.
func (t *LayeredTemplate) L0() Template { return t.l0 }

// L1 returns the runtime layer template, or nil.
func (t *LayeredTemplate) L1() Template { return t.l1 }

// L2 returns the instance-layer template.
func (t *LayeredTemplate) L2() Template { return t.l2 }

// SharedMemfilePath returns the path to the memfile that should be shared
// via SharedMemfileManager. Returns the highest shared layer's memfile
// path: L1 if present, else L0 if present, else "".
func (t *LayeredTemplate) SharedMemfilePath() string {
	if t.l1 != nil {
		return t.l1.Files().CacheSnapshotMemfile()
	}
	if t.l0 != nil {
		return t.l0.Files().CacheSnapshotMemfile()
	}
	return ""
}

// OverlayPath returns the path where the CoW overlay file should be stored.
// Only templates with an L2 (instance-private) layer have an overlay.
func (t *LayeredTemplate) OverlayPath() string {
	if t.l2 != nil {
		return t.l2.Files().CacheSnapfile() + ".cow_overlay"
	}
	return ""
}
