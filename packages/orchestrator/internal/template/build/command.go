package build

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
)

const httpTimeout = 600 * time.Second

func (b *TemplateBuilder) runCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	id string,
	sandboxID string,
	command string,
	runAsUser string,
	cwd *string,
) error {
	createAppReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd: "/bin/bash",
			Cwd: cwd,
			Args: []string{
				"-l", "-c", command,
			},
		},
	})

	hc := http.Client{
		Timeout: httpTimeout,
	}
	proxyHost := fmt.Sprintf("http://localhost%s", b.proxy.GetAddr())
	processC := processconnect.NewProcessClient(&hc, proxyHost)
	err := grpc.SetSandboxHeader(createAppReq.Header(), proxyHost, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to set sandbox header: %w", err)
	}
	grpc.SetUserHeader(createAppReq.Header(), runAsUser)

	processCtx, processCancel := context.WithCancel(ctx)
	defer processCancel()
	commandStream, err := processC.Start(processCtx, createAppReq)
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
				b.logStream(postProcessor, id, "stdout", string(data.GetStdout()))
				b.logStream(postProcessor, id, "stderr", string(data.GetStderr()))

			case e.GetEnd() != nil:
				end := e.GetEnd()
				name := fmt.Sprintf("exit %d", end.GetExitCode())
				b.logStream(postProcessor, id, name, end.GetStatus())

				if end.GetExitCode() != 0 {
					return fmt.Errorf("command failed: %s", end.GetStatus())
				}
			}
		}
	}
}

func (b *TemplateBuilder) logStream(postProcessor *writer.PostProcessor, id string, name string, content string) {
	if content == "" {
		return
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		msg := fmt.Sprintf("[%s] [%s]: %s", id, name, line)
		postProcessor.WriteMsg(msg)
		b.buildLogger.Info(msg)
	}
}
