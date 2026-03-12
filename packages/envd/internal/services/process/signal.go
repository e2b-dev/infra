package process

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"golang.org/x/sys/unix"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
)

func (s *Service) SendSignal(
	_ context.Context,
	req *connect.Request[rpc.SendSignalRequest],
) (*connect.Response[rpc.SendSignalResponse], error) {
	handler, err := s.getProcess(req.Msg.GetProcess())
	if err != nil {
		return nil, err
	}

	var signal unix.Signal
	switch req.Msg.GetSignal() {
	case rpc.Signal_SIGNAL_SIGKILL:
		signal = unix.SIGKILL
	case rpc.Signal_SIGNAL_SIGTERM:
		signal = unix.SIGTERM
	default:
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("invalid signal: %s", req.Msg.GetSignal()))
	}

	err = handler.SendSignal(signal)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error sending signal: %w", err))
	}

	return connect.NewResponse(&rpc.SendSignalResponse{}), nil
}
