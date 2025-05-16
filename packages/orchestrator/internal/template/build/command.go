package build

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
)

const httpTimeout = 600 * time.Second

func (b *TemplateBuilder) runCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	sandboxID string,
	cmdWait time.Duration,
	command string,
	runAsUser string,
) error {
	createAppReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd: "/bin/bash",
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
	err := grpc.SetSandboxHeader(createAppReq.Header(), proxyHost, sandboxID, b.clientID)
	if err != nil {
		return fmt.Errorf("failed to set sandbox header: %w", err)
	}
	grpc.SetUserHeader(createAppReq.Header(), runAsUser)

	processCtx, processCancel := context.WithCancel(ctx)
	defer processCancel()
	createAppStream, err := processC.Start(processCtx, createAppReq)
	if err != nil {
		return fmt.Errorf("error starting process: %w", err)
	}
	defer func() {
		processCancel()
		createAppStream.Close()
	}()

	cmdCtx, cancel := context.WithTimeout(ctx, cmdWait)
	defer cancel()

	msgCh, msgErrCh := grpc.StreamToChannel(cmdCtx, createAppStream)

	for {
		select {
		case <-cmdCtx.Done():
			return nil
		case err := <-msgErrCh:
			return err
		case msg := <-msgCh:
			e := msg.Event

			switch {
			case e.GetData() != nil:
				data := e.GetData()
				b.logStream(postProcessor, "stdout", string(data.GetStdout()))
				b.logStream(postProcessor, "stderr", string(data.GetStderr()))

			case e.GetEnd() != nil:
				end := e.GetEnd()
				name := fmt.Sprintf("exit %d", end.GetExitCode())
				b.logStream(postProcessor, name, end.GetStatus())

				if end.GetExitCode() != 0 {
					return fmt.Errorf("command failed: %s", end.GetStatus())
				}
			}
		}
	}
}

func (b *TemplateBuilder) logStream(postProcessor *writer.PostProcessor, name string, content string) {
	if content == "" {
		return
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		msg := fmt.Sprintf("[cmd] [%s]: %s", name, line)
		postProcessor.WriteMsg(msg)
		b.buildLogger.Info(msg)
	}
}
