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

func (b *TemplateBuilder) runReadyCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	template *TemplateConfig,
	sandboxID string,
) error {
	startTime := time.Now()
	defer func() {
		telemetry.ReportEvent(ctx, "waited for template ready", attribute.Float64("seconds", time.Since(startTime).Seconds()))
	}()

	postProcessor.WriteMsg("Waiting for template to be ready")

	if template.ReadyCmd == "" {
		template.ReadyCmd = getDefaultReadyCommand(template)
	}
	postProcessor.WriteMsg(fmt.Sprintf("[ready cmd]: %s", template.ReadyCmd))

	ctx, cancel := context.WithTimeout(ctx, readyCommandTimeout)
	defer cancel()

	// Start the ready check
wait:
	for {
		cwd := "/home/user"
		err := b.runCommand(
			ctx,
			postProcessor,
			"ready",
			sandboxID,
			template.ReadyCmd,
			"root",
			&cwd,
		)

		if err == nil {
			// Template is ready
			cancel()
			break wait
		} else {
			postProcessor.WriteMsg(fmt.Sprintf("Template is not ready yet: %v", err))
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				postProcessor.WriteMsg(fmt.Sprintf("Ready command timed out, exceeding %s", readyCommandTimeout))
			}
			// Template is ready, the start command finished before the ready command
			break wait
		case <-time.After(readyCommandRetryInterval):
			// Wait for readyCommandRetryInterval time before retrying the ready command
		}
	}

	// Cancel the context for the ready command if it is still running
	cancel()
	postProcessor.WriteMsg("Template is ready")

	return nil
}

func getDefaultReadyCommand(template *TemplateConfig) string {
	if template.StartCmd == "" {
		return fmt.Sprintf("sleep %d", 0)
	}

	// HACK: This is a temporary fix for a customer that needs a bigger time to start the command.
	// TODO: Remove this after we can add customizable wait time for building templates.
	// TODO: Make this user configurable, with health check too
	if template.TemplateId == "zegbt9dl3l2ixqem82mm" || template.TemplateId == "ot5bidkk3j2so2j02uuz" || template.TemplateId == "0zeou1s7agaytqitvmzc" {
		return fmt.Sprintf("sleep %d", int((120 * time.Second).Seconds()))
	}

	return fmt.Sprintf("sleep %d", int(defaultReadyWait.Seconds()))
}
