package server

import (
	"context"
	"errors"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (s *ServerStore) TemplateBuildDelete(ctx context.Context, in *templatemanager.TemplateBuildDeleteRequest) (*emptypb.Empty, error) {
	childCtx, childSpan := s.tracer.Start(ctx, "template-delete-request")
	defer childSpan.End()

	s.wg.Add(1)
	defer s.wg.Done()

	if in.TemplateID == "" || in.BuildID == "" {
		return nil, errors.New("template id and build id are required fields")
	}

	err := template.Delete(childCtx, s.tracer, s.artefactsRegistry, s.templateStorage, in.TemplateID, in.BuildID)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
