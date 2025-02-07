package sandbox

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sys/unix"

	"github.com/bits-and-blooms/bitset"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// sandboxSnapshotProvider implements SnapshotProvider using actual sandbox components
type sandboxSnapshotProvider struct {
	process *fc.Process
	uffd    *uffd.Uffd
	files   *storage.SandboxFiles
	rootfs  *rootfs.CowDevice
}

func (p *sandboxSnapshotProvider) PauseVM(ctx context.Context) error {
	return p.process.Pause(ctx, trace.SpanFromContext(ctx).TracerProvider().Tracer(""))
}

func (p *sandboxSnapshotProvider) DisableUffd() error {
	return p.uffd.Disable()
}

func (p *sandboxSnapshotProvider) CreateVMSnapshot(ctx context.Context, tracer trace.Tracer, snapfilePath, memfilePath string) error {
	return p.process.CreateSnapshot(ctx, tracer, snapfilePath, memfilePath)
}

func (p *sandboxSnapshotProvider) GetDirtyPages() *bitset.BitSet {
	return p.uffd.Dirty()
}

func (p *sandboxSnapshotProvider) GetMemfilePageSize() int64 {
	return p.files.MemfilePageSize()
}

func (p *sandboxSnapshotProvider) GetRootfsPath() (string, error) {
	return p.rootfs.Path()
}

func (p *sandboxSnapshotProvider) ExportRootfs(ctx context.Context, diffFile *build.LocalDiffFile, onStop func() error) (*bitset.BitSet, error) {
	return p.rootfs.Export(ctx, diffFile, onStop)
}

func (p *sandboxSnapshotProvider) FlushRootfs(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open rootfs path: %w", err)
	}
	defer file.Close()

	if err := unix.IoctlSetInt(int(file.Fd()), unix.BLKFLSBUF, 0); err != nil {
		return fmt.Errorf("ioctl BLKFLSBUF failed: %w", err)
	}

	if err := syscall.Fsync(int(file.Fd())); err != nil {
		return fmt.Errorf("failed to fsync rootfs path: %w", err)
	}

	return file.Sync()
}

// sandboxTemplateProvider implements TemplateProvider using actual template
type sandboxTemplateProvider struct {
	template template.Template
}

func (p *sandboxTemplateProvider) Memfile() (*template.Storage, error) {
	return p.template.Memfile()
}

func (p *sandboxTemplateProvider) Rootfs() (*template.Storage, error) {
	return p.template.Rootfs()
}
