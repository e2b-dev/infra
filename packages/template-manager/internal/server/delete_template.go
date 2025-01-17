package server

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/emptypb"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (s *serverStore) TemplateDelete(ctx context.Context, in *template_manager.TemplateDeleteRequest) (*emptypb.Empty, error) {
	// TODO: We need to delete all template builds and also keep track of snapshots that reference builds.
	return nil, fmt.Errorf("deleting template is not supported right now")
}
