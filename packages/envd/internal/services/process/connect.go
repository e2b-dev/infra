package process

import (
	"context"
	"errors"
	"fmt"

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

	data, dataCancel := proc.DataEvent.Fork(req.Msg.GetReplay())
	defer dataCancel()

	end, endCancel := proc.EndEvent.Fork(false)
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
			case item, ok := <-data:
				if !ok {
					break dataLoop
				}

				event, eventHandled := item.ValueWithAck()

				streamErr := stream.Send(&rpc.ConnectResponse{
					Event: &rpc.ProcessEvent{
						Event: &event,
					},
				})
				if streamErr != nil {
					cancel(connect.NewError(connect.CodeUnknown, fmt.Errorf("error sending data event: %w", streamErr)))

					eventHandled(false)

					return
				}

				eventHandled(true)

				resetKeepalive()
			}
		}

		select {
		case <-ctx.Done():
			cancel(ctx.Err())

			return
		case item, ok := <-end:
			if !ok {
				cancel(connect.NewError(connect.CodeUnknown, errors.New("end event channel closed before sending end event")))

				return
			}

			event := item.Value()

			streamErr := stream.Send(&rpc.ConnectResponse{
				Event: &rpc.ProcessEvent{
					Event: &event,
				},
			})
			if streamErr != nil {
				cancel(connect.NewError(connect.CodeUnknown, fmt.Errorf("error sending end event: %w", streamErr)))

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
