package build

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	defaultReadyWait = 20 * time.Second

	readyCommandRetryInterval = 2 * time.Second
	readyCommandTimeout       = 5 * time.Minute
)

func (b *TemplateBuilder) runStartCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	template *TemplateConfig,
	sandboxID string,
) error {
	postProcessor.WriteMsg("Running start command")

	if template.ReadyCmd == "" {
		template.ReadyCmd = getDefaultReadyCommand(template)
	}

	startCtx, startCancel := context.WithTimeout(ctx, readyCommandTimeout)
	defer startCancel()

	// Start the ready check
	go func() {
		for {
			cwd := "/home/user"
			err := b.runCommand(
				startCtx,
				postProcessor,
				sandboxID,
				template.ReadyCmd,
				"root",
				&cwd,
			)

			if err == nil {
				// Template is ready
				startCancel()
				return
			} else {
				postProcessor.WriteMsg(fmt.Sprintf("Template is not ready yet: %v", err))
			}

			select {
			case <-startCtx.Done():
				if errors.Is(startCtx.Err(), context.DeadlineExceeded) {
					postProcessor.WriteMsg(fmt.Sprintf("Ready command timed out, exceeding %s", readyCommandTimeout))
				}
				return
			case <-time.After(readyCommandRetryInterval):
				// Wait for readyCommandRetryInterval time before retrying the ready command
			}
		}
	}()

	cwd := "/home/user"
	err := b.runCommand(
		startCtx,
		postProcessor,
		sandboxID,
		template.StartCmd,
		"root",
		&cwd,
	)
	// If the ctx is canceled, the ready command succeeded and no start command await is necessary.
	if err != nil && !errors.Is(err, context.Canceled) {
		postProcessor.WriteMsg(fmt.Sprintf("Error while running start command: %v", err))
		return fmt.Errorf("error running start command: %w", err)
	}

	// Cancel the context for the ready command if it is still running
	startCancel()
	postProcessor.WriteMsg("Template is ready")
	telemetry.ReportEvent(ctx, "waited for start command", attribute.Float64("seconds", float64(defaultReadyWait/time.Second)))

	return nil
}

func getDefaultReadyCommand(template *TemplateConfig) string {
	// HACK: This is a temporary fix for a customer that needs a bigger time to start the command.
	// TODO: Remove this after we can add customizable wait time for building templates.
	// TODO: Make this user configurable, with health check too
	if template.TemplateId == "zegbt9dl3l2ixqem82mm" || template.TemplateId == "ot5bidkk3j2so2j02uuz" || template.TemplateId == "0zeou1s7agaytqitvmzc" {
		return fmt.Sprintf("sleep %d", int((120 * time.Second).Seconds()))
	}

	return fmt.Sprintf("sleep %d", int(defaultReadyWait.Seconds()))
}
