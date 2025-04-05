package server

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"
)

func (s *ServerStore) TemplateBuildDelete(ctx context.Context, in *template_manager.TemplateBuildDeleteRequest) (*emptypb.Empty, error) {
	childCtx, childSpan := s.tracer.Start(ctx, "template-delete-request")
	defer childSpan.End()

	s.wg.Add(1)
	defer s.wg.Done()

	err := template.Delete(childCtx, s.tracer, s.artifactRegistry, s.templateBuild, in.BuildID)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
