package volumes

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *Service) Stat(_ context.Context, request *orchestrator.StatRequest) (r *orchestrator.StatResponse, err error) {
	defer func() {
		err = s.processError(err)
	}()

	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	info, err := os.Lstat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat path: %w", err)
	}

	entry := toEntry(request.GetPath(), info)

	return &orchestrator.StatResponse{Entry: entry}, nil
}

func toEntry(volumeRelPath string, info os.FileInfo) *orchestrator.EntryInfo {
	var fType orchestrator.FileType
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		fType = orchestrator.FileType_FILE_TYPE_SYMLINK
	case info.IsDir():
		fType = orchestrator.FileType_FILE_TYPE_DIRECTORY
	default:
		fType = orchestrator.FileType_FILE_TYPE_FILE
	}

	if !strings.HasPrefix(volumeRelPath, "/") {
		volumeRelPath = "/" + volumeRelPath
	}

	entry := &orchestrator.EntryInfo{
		Name:         info.Name(),
		Type:         fType,
		Path:         volumeRelPath,
		Size:         info.Size(),
		Mode:         uint32(info.Mode() & os.ModePerm),
		ModifiedTime: timestamppb.New(info.ModTime()),
	}

	if base := getBase(info.Sys()); base != nil {
		entry.AccessedTime = toTimestamp(base.Atim)
		entry.CreatedTime = toTimestamp(base.Ctim)
		entry.ModifiedTime = toTimestamp(base.Mtim)
		entry.Uid = base.Uid
		entry.Gid = base.Gid
	}

	return entry
}

func toTimestamp(spec syscall.Timespec) *timestamppb.Timestamp {
	if spec.Sec == 0 && spec.Nsec == 0 {
		return nil
	}

	return timestamppb.New(time.Unix(spec.Sec, spec.Nsec))
}

func getBase(sys any) *syscall.Stat_t {
	st, _ := sys.(*syscall.Stat_t)

	return st
}
