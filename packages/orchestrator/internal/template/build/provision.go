package build

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	tt "text/template"

	"go.uber.org/zap"
	"go.uber.org/zap/zapio"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

//go:embed provision.sh
var provisionScriptFile string
var ProvisionScriptTemplate = tt.Must(tt.New("provisioning-script").Parse(provisionScriptFile))

const (
	// provisionScriptFileName is a path where the provision script stores it's exit code.
	provisionScriptResultPath = "/provision.result"
	provisionLogPrefix        = "[external] "
)

type ProvisionScriptParams struct {
	ResultPath string
}

func getProvisionScript(
	ctx context.Context,
	params ProvisionScriptParams,
) (string, error) {
	var scriptDef bytes.Buffer
	err := ProvisionScriptTemplate.Execute(&scriptDef, params)
	if err != nil {
		return "", fmt.Errorf("error executing provision script: %w", err)
	}
	telemetry.ReportEvent(ctx, "executed provision script env")

	return scriptDef.String(), nil
}

func (b *Builder) provisionSandbox(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	sandboxConfig *orchestrator.SandboxConfig,
	localTemplate *sbxtemplate.LocalTemplate,
	rootfsPath string,
	provisionScriptResultPath string,
	logExternalPrefix string,
) (e error) {
	ctx, childSpan := b.tracer.Start(ctx, "provision-sandbox")
	defer childSpan.End()

	zapWriter := &zapio.Writer{Log: postProcessor.Logger, Level: zap.DebugLevel}
	logsWriter := &writer.PrefixFilteredWriter{Writer: zapWriter, PrefixFilter: logExternalPrefix}
	defer logsWriter.Close()

	sbx, cleanup, err := sandbox.CreateSandbox(
		ctx,
		b.tracer,
		b.networkPool,
		b.devicePool,
		sandboxConfig,
		localTemplate,
		provisionTimeout,
		rootfsPath,
		fc.ProcessOptions{
			InitScriptPath: rootfs.BusyBoxInitPath,
			// Always show kernel logs during the provisioning phase,
			// the sandbox is then started with systemd and without kernel logs.
			KernelLogs: true,

			// Show provision script logs to the user
			Stdout: logsWriter,
			Stderr: logsWriter,
		},
	)
	defer func() {
		cleanupErr := cleanup.Run(ctx)
		if cleanupErr != nil {
			e = fmt.Errorf("error cleaning up sandbox: %w", cleanupErr)
		}
	}()
	if err != nil {
		return fmt.Errorf("error creating sandbox: %w", err)
	}
	err = sbx.WaitForExit(
		ctx,
		b.tracer,
	)
	if err != nil {
		return fmt.Errorf("failed to wait for sandbox start: %w", err)
	}
	postProcessor.Info("Sandbox template provisioned")

	// Verify the provisioning script exit status
	exitStatus, err := ext4.ReadFile(ctx, b.tracer, rootfsPath, provisionScriptResultPath)
	if err != nil {
		return fmt.Errorf("error reading provision result: %w", err)
	}
	defer ext4.RemoveFile(ctx, b.tracer, rootfsPath, provisionScriptResultPath)

	// Fallback to "1" if the file is empty or not found
	if exitStatus == "" {
		exitStatus = "1"
	}
	if exitStatus != "0" {
		return fmt.Errorf("provision script failed with exit status: %s", exitStatus)
	}

	return nil
}

func (b *Builder) enlargeDiskAfterProvisioning(
	ctx context.Context,
	template config.TemplateConfig,
	rootfs *block.Local,
) error {
	rootfsPath := rootfs.Path()

	// Resize rootfs to accommodate for the provisioning script size change
	rootfsFreeSpace, err := ext4.GetFreeSpace(ctx, b.tracer, rootfsPath, template.RootfsBlockSize())
	if err != nil {
		return fmt.Errorf("error getting free space: %w", err)
	}
	sizeDiff := template.DiskSizeMB<<constants.ToMBShift - rootfsFreeSpace
	zap.L().Debug("adding provision size diff to rootfs",
		zap.Int64("size_add", sizeDiff),
		zap.Int64("size_free", rootfsFreeSpace),
		zap.Int64("size_target", template.DiskSizeMB<<constants.ToMBShift),
	)
	if sizeDiff <= 0 {
		zap.L().Debug("no need to enlarge rootfs, skipping")
		return nil
	}
	rootfsFinalSize, err := ext4.Enlarge(ctx, b.tracer, rootfsPath, sizeDiff)
	if err != nil {
		// Debug filesystem stats on error
		cmd := exec.Command("tune2fs", "-l", rootfsPath)
		output, dErr := cmd.Output()
		zap.L().Error(string(output), zap.Error(dErr))

		return fmt.Errorf("error enlarging rootfs: %w", err)
	}

	// Check the rootfs filesystem corruption
	ext4Check, err := ext4.CheckIntegrity(rootfsPath, false)
	if err != nil {
		zap.L().Error("final enlarge filesystem ext4 integrity",
			zap.String("result", ext4Check),
			zap.Error(err),
		)

		// Occasionally there are Block bitmap differences. For this reason, we retry with fix.
		ext4Check, err := ext4.CheckIntegrity(rootfsPath, true)
		zap.L().Error("final enlarge filesystem ext4 integrity - retry with fix",
			zap.String("result", ext4Check),
			zap.Error(err),
		)
		if err != nil {
			return fmt.Errorf("error checking final enlarge filesystem integrity: %w", err)
		}
	} else {
		zap.L().Debug("final enlarge filesystem ext4 integrity",
			zap.String("result", ext4Check),
		)
	}

	stat, err := os.Stat(rootfsPath)
	if err != nil {
		return fmt.Errorf("error getting rootfs file info: %w", err)
	}

	// Safety check to ensure the size matches the file size
	if rootfsFinalSize != stat.Size() {
		return fmt.Errorf("size mismatch: expected %d, got %d", rootfsFinalSize, stat.Size())
	}

	err = rootfs.UpdateHeaderSize()
	if err != nil {
		return fmt.Errorf("error updating rootfs header size: %w", err)
	}

	return nil
}
