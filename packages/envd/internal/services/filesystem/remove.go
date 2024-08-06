package filesystem

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"

	"connectrpc.com/connect"
)

func (Service) Remove(ctx context.Context, req *connect.Request[rpc.RemoveRequest]) (*connect.Response[rpc.RemoveResponse], error) {
	u, err := permissions.GetAuthUser(ctx)
	if err != nil {
		return nil, err
	}

	resolvedPath, err := permissions.ExpandAndResolve(req.Msg.GetPath(), u)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	entry, err := os.Stat(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewResponse(&rpc.RemoveResponse{
				Entry: &rpc.EntryInfo{
					Name: path.Base(resolvedPath),
					Type: rpc.FileType_FILE_TYPE_UNSPECIFIED,
					Path: resolvedPath,
				},
			}), nil
		}

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error statting file or directory: %w", err))
	}

	err = os.RemoveAll(resolvedPath)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error removing file or directory: %w", err))
	}

	var t rpc.FileType
	if entry.IsDir() {
		t = rpc.FileType_FILE_TYPE_DIRECTORY
	} else {
		t = rpc.FileType_FILE_TYPE_FILE
	}

	return connect.NewResponse(&rpc.RemoveResponse{
		Entry: &rpc.EntryInfo{
			Name: path.Base(resolvedPath),
			Type: t,
			Path: resolvedPath,
		},
	}), nil
}
