package process

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/user"
	"strconv"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	"github.com/e2b-dev/infra/packages/envd/internal/services/process/handler"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"

	"connectrpc.com/connect"
)

func (s *Service) InitializeStartProcess(ctx context.Context, user *user.User, req *rpc.StartRequest) error {
	var err error

	ctx = logs.AddRequestIDToContext(ctx)

	defer s.logger.
		Err(err).
		Interface("request", req).
		Str(string(logs.OperationIDKey), ctx.Value(logs.OperationIDKey).(string)).
		Msg("Initialized startCmd")

	handlerL := s.logger.With().Str(string(logs.OperationIDKey), ctx.Value(logs.OperationIDKey).(string)).Logger()

	startProcCtx, startProcCancel := context.WithCancel(ctx)
	proc, err := handler.New(startProcCtx, user, req, &handlerL, nil, startProcCancel)
	if err != nil {
		return err
	}

	pid, err := proc.Start()
	if err != nil {
		return err
	}

	s.processes.Store(pid, proc)

	go func() {
		defer s.processes.Delete(pid)

		proc.Wait()
	}()

	return nil
}

func (s *Service) Start(ctx context.Context, req *connect.Request[rpc.StartRequest], stream *connect.ServerStream[rpc.StartResponse]) error {
	return logs.LogServerStreamWithoutEvents(ctx, s.logger, req, stream, s.handleStart)
}

func (s *Service) handleStart(ctx context.Context, req *connect.Request[rpc.StartRequest], stream *connect.ServerStream[rpc.StartResponse]) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	s.logger.Trace().Str(string(logs.OperationIDKey), ctx.Value(logs.OperationIDKey).(string)).Msg("Process start: Waiting for clock to sync")
	host.WaitForSync()
	s.logger.Trace().Str(string(logs.OperationIDKey), ctx.Value(logs.OperationIDKey).(string)).Msg("Process start: Clock synced")

	handlerL := s.logger.With().Str(string(logs.OperationIDKey), ctx.Value(logs.OperationIDKey).(string)).Logger()

	u, err := permissions.GetAuthUser(ctx)
	if err != nil {
		return err
	}

	timeout, err := determineTimeoutFromHeader(stream.Conn().RequestHeader())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Create a new context with a timeout if provided.
	// We do not want the command to be killed if the request context is cancelled
	procCtx, cancelProc := context.Background(), func() {}
	if timeout > 0 { // zero timeout means no timeout
		procCtx, cancelProc = context.WithTimeout(procCtx, timeout)
	}

	proc, err := handler.New(procCtx, u, req.Msg, &handlerL, s.envs, cancelProc)
	if err != nil {
		return err
	}

	exitChan := make(chan struct{})

	startMultiplexer := handler.NewMultiplexedChannel[rpc.ProcessEvent_Start](0)
	defer close(startMultiplexer.Source)

	start, startCancel := startMultiplexer.Fork()
	defer startCancel()

	data, dataCancel := proc.DataEvent.Fork()
	defer dataCancel()

	end, endCancel := proc.EndEvent.Fork()
	defer endCancel()

	go func() {
		defer close(exitChan)

		select {
		case <-ctx.Done():
			cancel(ctx.Err())

			return
		case event, ok := <-start:
			if !ok {
				cancel(connect.NewError(connect.CodeUnknown, errors.New("start event channel closed before sending start event")))

				return
			}

			streamErr := stream.Send(&rpc.StartResponse{
				Event: &rpc.ProcessEvent{
					Event: &event,
				},
			})
			if streamErr != nil {
				cancel(connect.NewError(connect.CodeUnknown, fmt.Errorf("error sending start event: %w", streamErr)))

				return
			}
		}

		keepaliveTicker, resetKeepalive := permissions.GetKeepAliveTicker(req)
		defer keepaliveTicker.Stop()

	dataLoop:
		for {
			select {
			case <-keepaliveTicker.C:
				streamErr := stream.Send(&rpc.StartResponse{
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

				streamErr := stream.Send(&rpc.StartResponse{
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

		select {
		case <-ctx.Done():
			cancel(ctx.Err())

			return
		case event, ok := <-end:
			if !ok {
				cancel(connect.NewError(connect.CodeUnknown, errors.New("end event channel closed before sending end event")))

				return
			}

			streamErr := stream.Send(&rpc.StartResponse{
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

	pid, err := proc.Start()
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	s.processes.Store(pid, proc)

	start <- rpc.ProcessEvent_Start{
		Start: &rpc.ProcessEvent_StartEvent{
			Pid: pid,
		},
	}

	go func() {
		defer s.processes.Delete(pid)

		proc.Wait()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-exitChan:
		return nil
	}
}

func determineTimeoutFromHeader(header http.Header) (time.Duration, error) {
	timeoutHeader := header.Get("Connect-Timeout-Ms")

	if timeoutHeader == "" {
		return 0, nil
	}

	timeout, err := strconv.Atoi(timeoutHeader)
	if err != nil {
		return 0, err
	}

	return time.Duration(timeout) * time.Millisecond, nil
}
