package filesystem

import (
	"context"
	"fmt"
	"os"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

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

	fileInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("file not found: %w", err))
		}

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error statting file: %w", err))
	}

	owner, group := getFileOwnership(fileInfo)
	fileMode := fileInfo.Mode()

	entry := &rpc.EntryInfo{
		Name:         fileInfo.Name(),
		Type:         getEntryType(fileInfo),
		Path:         path,
		Size:         fileInfo.Size(),
		Mode:         uint32(fileMode.Perm()),
		Permissions:  fileMode.String(),
		Owner:        owner,
		Group:        group,
		ModifiedTime: timestamppb.New(fileInfo.ModTime()),
	}

	return connect.NewResponse(&rpc.StatResponse{Entry: entry}), nil
}
