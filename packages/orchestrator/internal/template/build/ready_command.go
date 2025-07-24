package build

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	defaultReadyWait = 20 * time.Second

	readyCommandRetryInterval = 2 * time.Second
	readyCommandTimeout       = 5 * time.Minute
)

func (b *Builder) runReadyCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	metadata storage.TemplateFiles,
	template config.TemplateConfig,
	sandboxID string,
	cmdMetadata sandboxtools.CommandMetadata,
) error {
	ctx, span := b.tracer.Start(ctx, "run-ready-command")
	defer span.End()

	postProcessor.Info("Waiting for template to be ready")

	readyCmd := template.ReadyCmd
	if readyCmd == "" {
		readyCmd = getDefaultReadyCommand(metadata, template)
	}
	postProcessor.Info(fmt.Sprintf("[ready cmd]: %s", readyCmd))

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(ctx, readyCommandTimeout)
	defer cancel()

	// Start the ready check
	for {
		err := sandboxtools.RunCommandWithLogger(
			ctx,
			b.tracer,
			b.proxy,
			postProcessor,
			zapcore.InfoLevel,
			"ready",
			sandboxID,
			readyCmd,
			cmdMetadata,
		)

		if err == nil {
			postProcessor.Info("Template is ready")
			return nil
		} else {
			postProcessor.Info(fmt.Sprintf("Template is not ready: %v", err))
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("ready command timed out after %s", time.Since(startTime))
			}
			// Template is ready, the start command finished before the ready command
			postProcessor.Info("Template is ready")
			return nil
		case <-time.After(readyCommandRetryInterval):
			// Wait for readyCommandRetryInterval time before retrying the ready command
		}
	}
}

func getDefaultReadyCommand(metadata storage.TemplateFiles, template config.TemplateConfig) string {
	if template.StartCmd == "" {
		return fmt.Sprintf("sleep %d", 0)
	}

	// HACK: This is a temporary fix for a customer that needs a bigger time to start the command.
	// TODO: Remove this after we can add customizable wait time for building templates.
	// TODO: Make this user configurable, with health check too
	if metadata.TemplateID == "zegbt9dl3l2ixqem82mm" || metadata.TemplateID == "ot5bidkk3j2so2j02uuz" || metadata.TemplateID == "0zeou1s7agaytqitvmzc" {
		return fmt.Sprintf("sleep %d", int((120 * time.Second).Seconds()))
	}

	return fmt.Sprintf("sleep %d", int(defaultReadyWait.Seconds()))
}
