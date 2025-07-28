package filesystem

import (
	"context"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

func (Service) Stat(ctx context.Context, req *connect.Request[rpc.StatRequest]) (*connect.Response[rpc.StatResponse], error) {
	u, err := permissions.GetAuthUser(ctx)
	if err != nil {
		return nil, err
	}

	path, err := permissions.ExpandAndResolve(req.Msg.GetPath(), u)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	entry, err := entryInfo(path)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.StatResponse{Entry: entry}), nil
}
