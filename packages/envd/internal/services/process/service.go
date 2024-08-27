package process

import (
	"fmt"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/services/process/handler"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	spec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process/processconnect"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

type Service struct {
	processes *utils.Map[uint32, *handler.Handler]
	logger    *zerolog.Logger
	envs      *utils.Map[string, string]
}

func newService(l *zerolog.Logger, envs *utils.Map[string, string]) *Service {
	return &Service{
		logger:    l,
		processes: utils.NewMap[uint32, *handler.Handler](),
		envs:      envs,
	}
}

func Handle(server *chi.Mux, l *zerolog.Logger, envs *utils.Map[string, string]) *Service {
	service := newService(l, envs)

	interceptors := connect.WithInterceptors(logs.NewUnaryLogInterceptor(l))

	path, h := spec.NewProcessHandler(service, interceptors)

	server.Mount(path, h)

	return service
}

func (s *Service) getProcess(selector *rpc.ProcessSelector) (*handler.Handler, error) {
	var proc *handler.Handler

	switch selector.GetSelector().(type) {
	case *rpc.ProcessSelector_Pid:
		p, ok := s.processes.Load(selector.GetPid())
		if !ok {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("process with pid %d not found", selector.GetPid()))
		}

		proc = p
	case *rpc.ProcessSelector_Tag:
		tag := selector.GetTag()

		s.processes.Range(func(_ uint32, value *handler.Handler) bool {
			if value.Tag == nil {
				return true
			}

			if *value.Tag == tag {
				proc = value
				return true
			}

			return false
		})

		if proc == nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("process with tag %s not found", tag))
		}

	default:
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("invalid input type %T", selector))
	}

	return proc, nil
}
