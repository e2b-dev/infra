package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"syscall"

	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sys/unix"

	"github.com/bits-and-blooms/bitset"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
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

func (p *sandboxSnapshotProvider) GetDirtyUffd() *bitset.BitSet {
	return p.uffd.Dirty()
}

func (p *sandboxSnapshotProvider) GetMemfilePageSize() int64 {
	return p.files.MemfilePageSize()
}

func (p *sandboxSnapshotProvider) ExportRootfs(ctx context.Context, out io.Writer, stopSandbox func() error) (*bitset.BitSet, error) {
	return p.rootfs.Export(ctx, out, stopSandbox)
}

// FlushRootfsNBD flushes the NBD device to the local NBD backend
func (p *sandboxSnapshotProvider) FlushRootfsNBD() error {
	path, err := p.rootfs.Path()
	if err != nil {
		return fmt.Errorf("failed to get rootfs path: %w", err)
	}

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

func (p *sandboxTemplateProvider) MemfileHeader() (*header.Header, error) {
	memfile, err := p.template.Memfile()
	if err != nil {
		return nil, fmt.Errorf("failed to get memfile: %w", err)
	}
	return memfile.Header(), nil
}

func (p *sandboxTemplateProvider) RootfsHeader() (*header.Header, error) {
	rootfs, err := p.template.Rootfs()
	if err != nil {
		return nil, fmt.Errorf("failed to get rootfs: %w", err)
	}
	return rootfs.Header(), nil
}
