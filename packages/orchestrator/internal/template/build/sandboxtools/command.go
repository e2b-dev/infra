package sandboxtools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
)

const commandTimeout = 600 * time.Second

type CommandMetadata struct {
	User    string
	WorkDir *string
	EnvVars map[string]string
}

func RunCommand(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	logger *zap.Logger,
	postProcessor *writer.PostProcessor,
	id string,
	sandboxID string,
	command string,
	metadata CommandMetadata,
) error {
	return RunCommandWithConfirmation(
		ctx,
		tracer,
		proxy,
		logger,
		postProcessor,
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
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	logger *zap.Logger,
	postProcessor *writer.PostProcessor,
	id string,
	sandboxID string,
	command string,
	metadata CommandMetadata,
	confirmCh chan<- struct{},
) error {
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
		Timeout: commandTimeout,
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
			return ctx.Err()
		case err := <-msgErrCh:
			return err
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}
			e := msg.Event
			if e == nil {
				zap.L().Error("received nil command event")
				return nil
			}

			switch {
			case e.GetData() != nil:
				data := e.GetData()
				logStream(logger, postProcessor, id, "stdout", string(data.GetStdout()))
				logStream(logger, postProcessor, id, "stderr", string(data.GetStderr()))

			case e.GetEnd() != nil:
				end := e.GetEnd()
				success := end.GetExitCode() == 0

				if !success {
					name := fmt.Sprintf("exit %d", end.GetExitCode())
					logStream(logger, postProcessor, id, name, end.GetStatus())

					return fmt.Errorf("command failed: %s", end.GetStatus())
				}
			}
		}
	}
}

func logStream(logger *zap.Logger, postProcessor *writer.PostProcessor, id string, name string, content string) {
	if content == "" {
		return
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		msg := fmt.Sprintf("[%s] [%s]: %s", id, name, line)
		if postProcessor != nil {
			postProcessor.WriteMsg(msg)
		}
		if logger != nil {
			logger.Info(msg)
		}
	}
}
