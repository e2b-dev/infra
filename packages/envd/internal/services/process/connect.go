package process

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
)

func (s *Service) Connect(ctx context.Context, req *connect.Request[rpc.ConnectRequest], stream *connect.ServerStream[rpc.ConnectResponse]) error {
	return logs.LogServerStreamWithoutEvents(ctx, s.logger, req, stream, s.handleConnect)
}

func (s *Service) handleConnect(ctx context.Context, req *connect.Request[rpc.ConnectRequest], stream *connect.ServerStream[rpc.ConnectResponse]) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	proc, err := s.getProcess(req.Msg.GetProcess())
	if err != nil {
		return err
	}

	exitChan := make(chan struct{})

	data, dataCancel := proc.DataEvent.Fork()
	defer dataCancel()

	end, endCancel := proc.EndEvent.Fork()
	defer endCancel()

	streamErr := stream.Send(&rpc.ConnectResponse{
		Event: &rpc.ProcessEvent{
			Event: &rpc.ProcessEvent_Start{
				Start: &rpc.ProcessEvent_StartEvent{
					Pid: proc.Pid(),
				},
			},
		},
	})
	if streamErr != nil {
		return connect.NewError(connect.CodeUnknown, fmt.Errorf("error sending start event: %w", streamErr))
	}

	go func() {
		defer close(exitChan)

		keepaliveTicker, resetKeepalive := permissions.GetKeepAliveTicker(req)
		defer keepaliveTicker.Stop()

	dataLoop:
		for {
			select {
			case <-keepaliveTicker.C:
				streamErr := stream.Send(&rpc.ConnectResponse{
					Event: &rpc.ProcessEvent{
						Event: &rpc.ProcessEvent_Keepalive{
							Keepalive: &rpc.ProcessEvent_KeepAlive{},
						},
					},
				})
				if streamErr != nil {
					cancel(connect.NewError(connect.CodeUnknown, fmt.Errorf("error sending keepalive: %w", streamErr)))

					return
				}
			case <-ctx.Done():
				cancel(ctx.Err())

				return
			case event, ok := <-data:
				if !ok {
					break dataLoop
				}

				streamErr := stream.Send(&rpc.ConnectResponse{
					Event: &rpc.ProcessEvent{
						Event: &event,
					},
				})
				if streamErr != nil {
					cancel(connect.NewError(connect.CodeUnknown, fmt.Errorf("error sending data event: %w", streamErr)))

					return
				}

				resetKeepalive()
			}
		}

		// We MUST deliver the end event if the process has exited.
		// The data channel closing means all stdout/stderr is drained,
		// so the process is done or nearly done. Use a generous timeout
		// rather than racing against ctx.Done(), which can fire due to
		// transient network issues (proxy reset, NAT rebinding, etc).
		endCtx, endCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer endCancel()

		select {
		case <-endCtx.Done():
			cancel(fmt.Errorf("timed out waiting for process end event after data channel closed"))

			return
		case event, ok := <-end:
			if !ok {
				cancel(connect.NewError(connect.CodeUnknown, errors.New("end event channel closed before sending end event")))

				return
			}

			streamErr := stream.Send(&rpc.ConnectResponse{
				Event: &rpc.ProcessEvent{
					Event: &event,
				},
			})
			if streamErr != nil {
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-exitChan:
		return nil
	}
}
