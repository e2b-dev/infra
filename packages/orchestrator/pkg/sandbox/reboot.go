//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/constants"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/units"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	// minEnvdVersionForKVMClock is the minimum envd version that supports kvm-clock.
	minEnvdVersionForKVMClock = "0.2.11"

	// rebootEnvdTimeout bounds the systemd boot + envd start; a cold boot needs a
	// longer window than a memory resume (matches the template build's wait).
	rebootEnvdTimeout = 60 * time.Second
)

// RebootSandbox cold-boots a fresh Firecracker VM from the template's rootfs,
// without restoring guest memory. Used to resume filesystem-only snapshots:
// guest RAM, processes, and sockets are lost; only the filesystem survives.
// The sandbox is marked running only after envd is ready, matching
// ResumeSandbox's routing guarantees.
// IMPORTANT: You must Close() the sandbox after you are done with it.
func (f *Factory) RebootSandbox(
	ctx context.Context,
	t template.Template,
	config *Config,
	runtime RuntimeMetadata,
	startedAt time.Time,
	endAt time.Time,
	apiConfigToStore *orchestrator.SandboxConfig,
) (*Sandbox, error) {
	ctx, span := tracer.Start(ctx, "reboot sandbox")
	defer span.End()

	buildID, err := uuid.Parse(t.Files().BuildID)
	if err != nil {
		return nil, fmt.Errorf("parse build ID: %w", err)
	}

	// The template memfile is masked with an empty one: it is used only for
	// sizing NoopMemory — guest RAM is FC's own fresh anonymous memory.
	pageSize := int64(header.PageSize)
	if config.HugePages {
		pageSize = int64(header.HugepageSize)
	}
	memfile, err := block.NewEmpty(units.MBToBytes(config.RamMB), pageSize, buildID)
	if err != nil {
		return nil, fmt.Errorf("create empty memfile: %w", err)
	}

	maskedTemplate := template.NewMaskTemplate(t, template.WithMemfile(memfile))

	kvmClock, err := utils.IsGTEVersion(config.Envd.Version, minEnvdVersionForKVMClock)
	if err != nil {
		return nil, fmt.Errorf("compare envd version: %w", err)
	}

	// Sync IO engine so no async writes are in flight if this sandbox is
	// paused again.
	ioEngine := models.DriveIoEngineSync

	timeout := time.Until(endAt)
	if timeout <= 0 {
		return nil, fmt.Errorf("sandbox end time %s is in the past", endAt)
	}

	sbx, err := f.CreateSandbox(
		ctx,
		config,
		runtime,
		maskedTemplate,
		timeout,
		// Empty rootfs cache path selects the NBD provider — the same provider a
		// memory resume uses — so guest TRIM/WRITE_ZEROES keep working and a
		// later pause exports the overlay diff exactly like a normal resume.
		"",
		fc.ProcessOptions{
			InitScriptPath: constants.SystemdInitPath,
			KvmClock:       kvmClock,
			IoEngine:       &ioEngine,
		},
		apiConfigToStore,
		nil,
		// Defer marking running until envd is ready so the rebooted sandbox
		// isn't visible/routable before it can serve traffic.
		WithDeferredMarkRunning(),
	)
	if err != nil {
		return nil, fmt.Errorf("create sandbox from rootfs: %w", err)
	}

	// CreateSandbox anchors the lifetime to now; honor the caller's absolute
	// window so queue delay can't extend the end time.
	sbx.SetStartedAt(startedAt)
	sbx.SetEndAt(endAt)

	if err := sbx.WaitForEnvd(ctx, rebootEnvdTimeout); err != nil {
		closeErr := sbx.Close(context.WithoutCancel(ctx))

		return nil, errors.Join(fmt.Errorf("wait for envd after reboot: %w", err), closeErr)
	}

	f.Sandboxes.MarkRunning(ctx, sbx)

	go sbx.Checks.Start(context.WithoutCancel(ctx))

	return sbx, nil
}
