package build

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/sync/errgroup"

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
	grpc.SetSandboxHeader(createAppReq.Header(), proxyHost, sandboxID, b.clientID)
	grpc.SetUserHeader(createAppReq.Header(), "user")
	createAppStream, err := processC.Start(ctx, createAppReq)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while starting process: %v", err))
		return fmt.Errorf("error starting process: %w", err)
	}
	defer createAppStream.Close()

	startCmdCtx, cancel := context.WithTimeout(ctx, cmdWait)
	defer cancel()

	msgCh, msgErrCh := grpc.StreamToChannel(startCmdCtx, createAppStream)

	var g errgroup.Group
	g.Go(func() error {
		for {
			select {
			case <-startCmdCtx.Done():
				return nil
			case err := <-msgErrCh:
				return err
			case msg := <-msgCh:
				fmtMsg := fmt.Sprintf("[cmd]: %s", msg)
				postProcessor.WriteMsg(fmtMsg)
				b.buildLogger.Info(fmtMsg)
			}
		}
	})
	return g.Wait()
}
