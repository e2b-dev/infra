//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/constants"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/units"
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
// ResumeSandbox's routing guarantees; endAt is the caller's absolute end time.
// procOpts, if any, adjust the fc.ProcessOptions of the cold boot after the
// defaults are set (e.g. debugging tools forwarding the guest kernel console).
// IMPORTANT: You must Close() the sandbox after you are done with it.
func (f *Factory) RebootSandbox(
	ctx context.Context,
	t template.Template,
	config *Config,
	runtime RuntimeMetadata,
	endAt time.Time,
	apiConfigToStore *orchestrator.SandboxConfig,
	procOpts ...func(*fc.ProcessOptions),
) (*Sandbox, error) {
	ctx, span := tracer.Start(ctx, "reboot sandbox")
	defer span.End()

	buildID, err := uuid.Parse(t.Files().BuildID)
	if err != nil {
		return nil, fmt.Errorf("parse build ID: %w", err)
	}

	// Safety gate: only filesystem-only snapshots are safe to cold-boot from. A
	// memory snapshot's rootfs may be missing writes that lived only in the
	// guest page cache (restored on a memory resume), so rebooting it would
	// serve an inconsistent disk. Refuse unless the snapshot is marked fs-only.
	meta, err := t.Metadata()
	if err != nil {
		return nil, fmt.Errorf("get template metadata: %w", err)
	}
	if !meta.IsFilesystemOnly() {
		return nil, fmt.Errorf("refusing to reboot build %s: not a filesystem-only snapshot", buildID)
	}

	// A cold boot starts envd with no prior state, so unlike a memory resume it
	// can't inherit the template's default user/workdir from restored RAM — they
	// must be re-sent via /init, or envd falls back to root and /root. Mirror
	// finalize's build-time logic (Context.User, with a "user" fallback for
	// pre-V2 builds that didn't record one).
	if config.Envd.DefaultUser == nil {
		defaultUser := meta.Context.User
		if defaultUser == "" {
			defaultUser = "user"
		}
		config.Envd.DefaultUser = &defaultUser
	}
	if config.Envd.DefaultWorkdir == nil {
		config.Envd.DefaultWorkdir = meta.Context.WorkDir
	}

	// The masked empty memfile is used only for sizing NoopMemory — guest RAM
	// is FC's own fresh anonymous memory.
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

	// Sync IO engine so no async writes are in flight if the sandbox is paused again.
	ioEngine := models.DriveIoEngineSync

	// Always write MMDS metadata for a reboot so the cold-booted envd can
	// authenticate /init like a memory resume. An empty token hashes to the
	// "no token" value, matching ResumeSandbox's behavior for non-secure sandboxes.
	accessToken := ""
	if config.Envd.AccessToken != nil {
		accessToken = *config.Envd.AccessToken
	}

	timeout := time.Until(endAt)
	if timeout <= 0 {
		return nil, fmt.Errorf("sandbox end time %s is in the past", endAt)
	}

	processOptions := fc.ProcessOptions{
		InitScriptPath: constants.SystemdInitPath,
		KvmClock:       kvmClock,
		IoEngine:       &ioEngine,
		AccessToken:    &accessToken,
	}
	for _, opt := range procOpts {
		opt(&processOptions)
	}

	// A filesystem-only snapshot captured without FIFREEZE support may have a torn
	// ext4 journal, which fails this cold boot with "JBD2: recovery failed" /
	// "error loading journal". Recover it before Firecracker boots. The trigger is
	// the EXT4_FEATURE_INCOMPAT_RECOVER superblock bit, which is set whenever the
	// journal needs replay — so this also runs on healthy non-FIFREEZE snapshots,
	// where e2fsck simply replays cleanly. Doing it here heals snapshots already
	// captured torn, not just new ones. Gated behind a flag.
	var preBoot PreBootFn
	if f.featureFlags.BoolFlag(ctx, featureflags.FsOnlyJournalRecoveryFlag,
		featureflags.SandboxContext(runtime.SandboxID),
		featureflags.TemplateContext(runtime.TemplateID),
	) {
		preBoot = func(ctx context.Context, rootfsPath string) error {
			// Bound e2fsck on its own deadline, decoupled from the request context:
			// recovery (up to tens of seconds on a cold NBD) must not eat the whole
			// request budget and starve the envd wait that follows, and a request
			// timeout must not kill e2fsck mid-write. On timeout we fall through and
			// boot (best-effort, below).
			recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rootfs.JournalRecoveryTimeout)
			defer cancel()

			recovered, err := rootfs.RecoverExt4Journal(recCtx, rootfsPath)
			if err != nil {
				// Best-effort: don't abort a boot the guest kernel can still complete
				// via its own jbd2 replay. e2fsck can fail for reasons unrelated to a
				// torn journal (missing binary, slow full check over NBD, a context
				// timeout); in those cases booting is strictly no worse than not having
				// this hook. A genuinely torn journal that e2fsck couldn't fix will
				// fail the boot either way.
				logger.L().Warn(ctx, "ext4 journal recovery before reboot failed; booting anyway",
					logger.WithSandboxID(runtime.SandboxID),
					logger.WithBuildID(buildID.String()),
					zap.Error(err),
				)

				return nil
			}
			if recovered {
				logger.L().Info(ctx, "recovered torn rootfs journal before reboot",
					logger.WithSandboxID(runtime.SandboxID),
					logger.WithBuildID(buildID.String()),
				)
			}

			return nil
		}
	}

	sbx, err := f.CreateSandbox(
		ctx,
		config,
		runtime,
		maskedTemplate,
		timeout,
		// Empty rootfs cache path selects the NBD provider, same as a memory
		// resume, so guest TRIM keeps working and a later pause exports the
		// overlay diff exactly like a normal resume.
		"",
		processOptions,
		apiConfigToStore,
		preBoot,
		WithDeferredMarkRunning(),
		withNetworkAssignReason(NetworkAssignReasonReboot),
	)
	if err != nil {
		return nil, fmt.Errorf("create sandbox from rootfs: %w", err)
	}

	// CreateSandbox anchors the lifetime to now; honor the caller's absolute end
	// time so queue delay can't extend the TTL.
	sbx.SetEndAt(endAt)

	if err := sbx.WaitForEnvd(ctx, StartTypeReboot, rebootEnvdTimeout); err != nil {
		closeErr := sbx.Close(context.WithoutCancel(ctx))

		return nil, errors.Join(fmt.Errorf("wait for envd after reboot: %w", err), closeErr)
	}

	f.Sandboxes.MarkRunning(ctx, sbx)

	go sbx.Checks.Start(context.WithoutCancel(ctx))

	return sbx, nil
}
