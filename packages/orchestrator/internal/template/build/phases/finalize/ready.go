package finalize

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
)

const (
	defaultReadyWait = 20 * time.Second

	readyCommandRetryInterval = 2 * time.Second
	readyCommandTimeout       = 5 * time.Minute
)

func (ppb *PostProcessingBuilder) runReadyCommand(
	ctx context.Context,
	sandboxID string,
	readyCmd string,
	cmdMetadata metadata.CommandMetadata,
) error {
	ctx, span := ppb.tracer.Start(ctx, "run-ready-command")
	defer span.End()

	ppb.UserLogger.Info("Waiting for template to be ready")

	ppb.UserLogger.Info(fmt.Sprintf("[ready cmd]: %s", readyCmd))

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(ctx, readyCommandTimeout)
	defer cancel()

	// Start the ready check
	for {
		err := sandboxtools.RunCommandWithLogger(
			ctx,
			ppb.tracer,
			ppb.proxy,
			ppb.UserLogger,
			zapcore.InfoLevel,
			"ready",
			sandboxID,
			readyCmd,
			cmdMetadata,
		)

		if err == nil {
			ppb.UserLogger.Info("Template is ready")
			return nil
		} else {
			ppb.UserLogger.Info(fmt.Sprintf("Template is not ready: %v", err))
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("ready command timed out after %s", time.Since(startTime))
			}
			// Template is ready, the start command finished before the ready command
			ppb.UserLogger.Info("Template is ready")
			return nil
		case <-time.After(readyCommandRetryInterval):
			// Wait for readyCommandRetryInterval time before retrying the ready command
		}
	}
}

func GetDefaultReadyCommand(templateID string) string {
	// HACK: This is a temporary fix for a customer that needs a bigger time to start the command.
	// TODO: Remove this after we can add customizable wait time for building templates.
	// TODO: Make this user configurable, with health check too
	if templateID == "zegbt9dl3l2ixqem82mm" || templateID == "ot5bidkk3j2so2j02uuz" || templateID == "0zeou1s7agaytqitvmzc" {
		return fmt.Sprintf("sleep %d", int((120 * time.Second).Seconds()))
	}

	return fmt.Sprintf("sleep %d", int(defaultReadyWait.Seconds()))
}
