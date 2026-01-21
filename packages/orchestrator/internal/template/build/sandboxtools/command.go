package sandboxtools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const commandHardTimeout = 1 * time.Hour

func RunCommandWithOutput(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	command string,
	metadata metadata.Context,
	processOutput func(stdout, stderr string),
) error {
	return runCommandWithAllOptions(
		ctx,
		proxy,
		sandboxID,
		command,
		metadata,
		// No confirmation needed for this command
		make(chan struct{}),
		processOutput,
	)
}

func RunCommand(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	command string,
	metadata metadata.Context,
) error {
	return runCommandWithAllOptions(
		ctx,
		proxy,
		sandboxID,
		command,
		metadata,
		// No confirmation needed for this command
		make(chan struct{}),
		func(_, _ string) {},
	)
}

func RunCommandWithLogger(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	logger logger.Logger,
	lvl zapcore.Level,
	id string,
	sandboxID string,
	command string,
	metadata metadata.Context,
) error {
	return RunCommandWithConfirmation(
		ctx,
		proxy,
		logger,
		lvl,
		id,
		sandboxID,
		command,
		metadata,
		// No confirmation needed for this command
		make(chan struct{}),
	)
}

func RunCommandWithConfirmation(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	logger logger.Logger,
	lvl zapcore.Level,
	id string,
	sandboxID string,
	command string,
	metadata metadata.Context,
	confirmCh chan<- struct{},
) error {
	return runCommandWithAllOptions(
		ctx,
		proxy,
		sandboxID,
		command,
		metadata,
		confirmCh,
		func(stdout, stderr string) {
			logStream(ctx, logger, lvl, id, "stdout", stdout)
			logStream(ctx, logger, lvl, id, "stderr", stderr)
		},
	)
}

func runCommandWithAllOptions(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	command string,
	metadata metadata.Context,
	confirmCh chan<- struct{},
	processOutput func(stdout, stderr string),
) (e error) {
	ctx, span := tracer.Start(ctx, "run command", trace.WithAttributes(attribute.String("command", command), telemetry.WithSandboxID(sandboxID)))
	defer span.End()
	defer func() {
		if e != nil {
			span.RecordError(e)
			span.SetStatus(codes.Error, e.Error())
		}
	}()

	runCmdReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd: "/bin/bash",
			Cwd: metadata.WorkDir,
			Args: []string{
				"-l", "-c", command,
			},
			Envs: metadata.EnvVars,
		},
	})

	hc := http.Client{
		Timeout:   commandHardTimeout,
		Transport: sandbox.SandboxHttpTransport,
	}

	proxyHost := fmt.Sprintf("http://localhost%s", proxy.GetAddr())
	processC := processconnect.NewProcessClient(&hc, proxyHost)
	err := grpc.SetSandboxHeader(runCmdReq.Header(), proxyHost, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to set sandbox header: %w", err)
	}
	grpc.SetUserHeader(runCmdReq.Header(), metadata.User)

	processCtx, processCancel := context.WithCancel(ctx)
	defer processCancel()
	commandStream, err := processC.Start(processCtx, runCmdReq)
	// Confirm the command has executed before proceeding
	close(confirmCh)
	if err != nil {
		return fmt.Errorf("error starting process: %w", err)
	}
	defer func() {
		processCancel()
		commandStream.Close()
	}()

	msgCh, msgErrCh := grpc.StreamToChannel(ctx, commandStream)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context: %w", ctx.Err())
		case err := <-msgErrCh:
			return fmt.Errorf("command failed: %w", err)
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}
			e := msg.GetEvent()
			if e == nil {
				logger.L().Error(ctx, "received nil command event")

				return nil
			}

			switch {
			case e.GetData() != nil:
				data := e.GetData()
				processOutput(string(data.GetStdout()), string(data.GetStderr()))

			case e.GetEnd() != nil:
				end := e.GetEnd()
				success := end.GetExitCode() == 0

				if !success {
					return errors.New(end.GetStatus())
				}
			}
		}
	}
}

func logStream(ctx context.Context, logger logger.Logger, lvl zapcore.Level, id string, name string, content string) {
	if logger == nil {
		return
	}

	if content == "" {
		return
	}
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		msg := fmt.Sprintf("[%s] [%s]: %s", id, name, line)
		logger.Log(ctx, lvl, msg)
	}
}

// syncChangesToDisk synchronizes filesystem changes to the filesystem
// This is useful to ensure that all changes made in the sandbox are written to disk
// to be able to re-create the sandbox without resume.
func SyncChangesToDisk(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	sandboxID string,
) error {
	return RunCommand(
		ctx,
		proxy,
		sandboxID,
		"/usr/bin/busybox sync",
		metadata.Context{
			User: "root",
		},
	)
}
