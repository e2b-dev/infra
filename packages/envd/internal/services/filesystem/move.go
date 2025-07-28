package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

func (Service) Move(ctx context.Context, req *connect.Request[rpc.MoveRequest]) (*connect.Response[rpc.MoveResponse], error) {
	u, err := permissions.GetAuthUser(ctx)
	if err != nil {
		return nil, err
	}

	source, err := permissions.ExpandAndResolve(req.Msg.GetSource(), u)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	destination, err := permissions.ExpandAndResolve(req.Msg.GetDestination(), u)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	uid, gid, userErr := permissions.GetUserIds(u)
	if userErr != nil {
		return nil, connect.NewError(connect.CodeInternal, userErr)
	}

	userErr = permissions.EnsureDirs(filepath.Dir(destination), int(uid), int(gid))
	if userErr != nil {
		return nil, connect.NewError(connect.CodeInternal, userErr)
	}

	err = os.Rename(source, destination)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error renaming: %w", err))
	}

	entry, err := entryInfo(destination)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.MoveResponse{
		Entry: entry,
	}), nil
}
