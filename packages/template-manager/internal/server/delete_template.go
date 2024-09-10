package server

import (
	"context"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"

	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *serverStore) TemplateDelete(ctx context.Context, in *template_manager.TemplateDeleteRequest) (*emptypb.Empty, error) {
	childCtx, childSpan := s.tracer.Start(ctx, "template-delete-request")
	defer childSpan.End()

	err := template.Delete(childCtx, s.tracer, s.artifactRegistry, s.templateStorage, in.TemplateID)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
