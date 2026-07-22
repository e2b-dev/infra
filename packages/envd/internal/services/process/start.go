package process

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	"github.com/e2b-dev/infra/packages/envd/internal/services/process/handler"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
)

func (s *Service) Start(ctx context.Context, req *connect.Request[rpc.StartRequest], stream *connect.ServerStream[rpc.StartResponse]) error {
	return logs.LogServerStreamWithoutEvents(ctx, s.logger, req, stream, s.handleStart)
}

func (s *Service) handleStart(ctx context.Context, req *connect.Request[rpc.StartRequest], stream *connect.ServerStream[rpc.StartResponse]) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	handlerL := s.logger.With().Str(string(logs.OperationIDKey), ctx.Value(logs.OperationIDKey).(string)).Logger()

	u, err := permissions.GetAuthUser(ctx, s.defaults.User)
	if err != nil {
		return err
	}

	requestTimeout, err := determineTimeoutFromHeader(stream.Conn().RequestHeader())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Create a new context with a timeout if provided.
	// We do not want the command to be killed if the request context is cancelled
	procCtx, cancelProc := context.Background(), func() {}
	if requestTimeout > 0 { // zero timeout means no timeout
		procCtx, cancelProc = context.WithTimeout(procCtx, requestTimeout)
	}

	proc, err := handler.New( //nolint:contextcheck // TODO: fix this later
		procCtx,
		u,
		req.Msg,
		&handlerL,
		s.defaults,
		s.cgroupManager,
		cancelProc,
	)
	if err != nil {
		// Ensure the process cancel is called to cleanup resources.
		cancelProc()

		return err
	}

	exitChan := make(chan struct{})

	// Buffered so the send below never blocks when the receiver
	// goroutine has already exited on a cancelled context.
	start := make(chan rpc.ProcessEvent_Start, 1)

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
		case event := <-start:
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

	pid, err := proc.Start(requestTimeout)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Drop any retained exit left over from a previous process that used this
	// PID, so a Connect to the new process can't be served the old exit code.
	s.terminated.Delete(pid)
	s.processes.Store(pid, proc)
	s.trackTermination(pid, proc)

	start <- rpc.ProcessEvent_Start{
		Start: &rpc.ProcessEvent_StartEvent{
			Pid: pid,
		},
	}

	go func() {
		// Reap the process. Removal from s.processes is owned solely by
		// finalizeTermination (via trackTermination / the reaper OnExit hook),
		// which retains the exit code BEFORE deleting and is identity-guarded
		// against PID reuse. Deleting here too would race that retention away and
		// lose the exit for a late Connect or the pre-upgrade handover.
		proc.Wait()
	}()

	// Wait for the sender goroutine; returning early panics envd.
	<-exitChan

	return ctx.Err()
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
