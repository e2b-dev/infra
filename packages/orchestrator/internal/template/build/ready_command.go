package build

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/templateconfig"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
)

const (
	defaultReadyWait = 20 * time.Second

	readyCommandRetryInterval = 2 * time.Second
	readyCommandTimeout       = 5 * time.Minute
)

func (b *TemplateBuilder) runReadyCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	template *templateconfig.TemplateConfig,
	sandboxID string,
	envVars map[string]string,
) error {
	ctx, span := b.tracer.Start(ctx, "run-ready-command")
	defer span.End()

	postProcessor.WriteMsg("Waiting for template to be ready")

	if template.ReadyCmd == "" {
		template.ReadyCmd = getDefaultReadyCommand(template)
	}
	postProcessor.WriteMsg(fmt.Sprintf("[ready cmd]: %s", template.ReadyCmd))

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(ctx, readyCommandTimeout)
	defer cancel()

	// Start the ready check
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
			envVars,
		)

		if err == nil {
			postProcessor.WriteMsg("Template is ready")
			return nil
		} else {
			postProcessor.WriteMsg(fmt.Sprintf("Template is not ready: %v", err))
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("ready command timed out after %s", time.Since(startTime))
			}
			// Template is ready, the start command finished before the ready command
			postProcessor.WriteMsg("Template is ready")
			return nil
		case <-time.After(readyCommandRetryInterval):
			// Wait for readyCommandRetryInterval time before retrying the ready command
		}
	}
}

func getDefaultReadyCommand(template *templateconfig.TemplateConfig) string {
	if template.StartCmd == "" {
		return fmt.Sprintf("sleep %d", 0)
	}

	// HACK: This is a temporary fix for a customer that needs a bigger time to start the command.
	// TODO: Remove this after we can add customizable wait time for building templates.
	// TODO: Make this user configurable, with health check too
	if template.TemplateID == "zegbt9dl3l2ixqem82mm" || template.TemplateID == "ot5bidkk3j2so2j02uuz" || template.TemplateID == "0zeou1s7agaytqitvmzc" {
		return fmt.Sprintf("sleep %d", int((120 * time.Second).Seconds()))
	}

	return fmt.Sprintf("sleep %d", int(defaultReadyWait.Seconds()))
}
