package server

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"
)

func (s *serverStore) TemplateDelete(ctx context.Context, in *template_manager.TemplateBuildDeleteRequest) (*emptypb.Empty, error) {
	childCtx, childSpan := s.tracer.Start(ctx, "template-delete-request")
	defer childSpan.End()

	err := template.Delete(childCtx, s.tracer, s.artifactRegistry, s.templateStorage, in.BuildID)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
