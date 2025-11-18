package base

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	tt "text/template"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapio"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/filesystem"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	provisionTimeout = 5 * time.Minute

	exitPrefix = "EXIT:"
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
	BusyBox    string
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

func (bb *BaseBuilder) provisionSandbox(
	ctx context.Context,
	userLogger *zap.Logger,
	sandboxConfig sandbox.Config,
	sandboxRuntime sandbox.RuntimeMetadata,
	fcVersions fc.FirecrackerVersions,
	localTemplate *sbxtemplate.LocalTemplate,
	rootfsPath string,
	logExternalPrefix string,
) (e error) {
	ctx, childSpan := tracer.Start(ctx, "provision-sandbox")
	defer childSpan.End()

	zapWriter := &zapio.Writer{Log: userLogger, Level: zap.DebugLevel}
	prefixedLogsWriter := &writer.PrefixFilteredWriter{Writer: zapWriter, PrefixFilter: logExternalPrefix}
	defer prefixedLogsWriter.Close()

	exitCodeReader, exitCodeWriter := io.Pipe()
	defer exitCodeWriter.Close()

	// read all incoming logs and detect message "EXIT:X" or logExternalPrefix + "EXIT:X" where X is the exit code
	scanner := bufio.NewScanner(exitCodeReader)
	done := utils.NewErrorOnce()
	go func() (e error) {
		defer io.Copy(io.Discard, exitCodeReader)
		defer func() {
			done.SetError(e)
		}()

		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, exitPrefix) {
				exitStatus := strings.TrimPrefix(line, exitPrefix)
				if exitStatus == "0" {
					// Success exit code
					return nil
				}

				return fmt.Errorf("exit status: %s", exitStatus)
			}
		}

		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		return errors.New("exit code not detected")
	}()

	logsWriter := io.MultiWriter(prefixedLogsWriter, exitCodeWriter)

	sbx, err := bb.sandboxFactory.CreateSandbox(
		ctx,
		sandboxConfig,
		sandboxRuntime,
		fcVersions,
		localTemplate,
		provisionTimeout,
		rootfsPath,
		fc.ProcessOptions{
			InitScriptPath: rootfs.BusyBoxInitPath,
			// Always show kernel logs during the provisioning phase,
			// the sandbox is then started with systemd and without kernel logs.
			KernelLogs: true,

			KvmClock: true,

			// Show provision script logs to the user
			Stdout: logsWriter,
			Stderr: logsWriter,
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("error creating sandbox: %w", err)
	}
	defer sbx.Close(ctx)

	if err := done.WaitWithContext(ctx); err != nil {
		return fmt.Errorf("error waiting for provisioning sandbox: %w", err)
	}

	userLogger.Info("Sandbox template provisioned")

	_, err = sbx.Pause(ctx, metadata.Template{
		Template: storage.TemplateFiles{
			BuildID:            uuid.NewString(),
			KernelVersion:      fcVersions.KernelVersion,
			FirecrackerVersion: fcVersions.FirecrackerVersion,
		},
	})
	if err != nil {
		return fmt.Errorf("error pausing provisioned sandbox: %w", err)
	}

	userLogger.Info("Changes flushed")

	return nil
}

func (bb *BaseBuilder) enlargeDiskAfterProvisioning(
	ctx context.Context,
	template config.TemplateConfig,
	rootfs *block.Local,
) error {
	rootfsPath := rootfs.Path()

	// Resize rootfs to accommodate for the provisioning script size change
	rootfsFreeSpace, err := filesystem.GetFreeSpace(ctx, rootfsPath, template.RootfsBlockSize())
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
	rootfsFinalSize, err := filesystem.Enlarge(ctx, rootfsPath, sizeDiff)
	if err != nil {
		// Debug filesystem stats on error
		cmd := exec.CommandContext(ctx, "tune2fs", "-l", rootfsPath)
		output, dErr := cmd.Output()
		zap.L().Error(string(output), zap.Error(dErr))

		return fmt.Errorf("error enlarging rootfs: %w", err)
	}

	// Check the rootfs filesystem corruption
	ext4Check, err := filesystem.CheckIntegrity(ctx, rootfsPath, false)
	if err != nil {
		zap.L().Error("final enlarge filesystem ext4 integrity",
			zap.String("result", ext4Check),
			zap.Error(err),
		)

		// Occasionally there are Block bitmap differences. For this reason, we retry with fix.
		ext4Check, err := filesystem.CheckIntegrity(ctx, rootfsPath, true)
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
