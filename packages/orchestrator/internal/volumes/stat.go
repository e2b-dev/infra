package volumes

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) Stat(_ context.Context, request *orchestrator.StatRequest) (*orchestrator.StatResponse, error) {
	basePath, statusErr := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	fullPath := filepath.Join(basePath, request.GetPath())

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, v.processError(err)
	}

	entry := toEntry(info)

	return &orchestrator.StatResponse{Entry: entry}, nil
}

func toEntry(info os.FileInfo) *orchestrator.EntryInfo {
	fType := orchestrator.FileType_FILE_TYPE_FILE
	if info.IsDir() {
		fType = orchestrator.FileType_FILE_TYPE_DIRECTORY
	}

	entry := &orchestrator.EntryInfo{
		Name:         filepath.Base(info.Name()),
		Type:         fType,
		Path:         info.Name(),
		Size:         info.Size(),
		Mode:         uint32(info.Mode()),
		Permissions:  uint32(info.Mode().Perm()),
		ModifiedTime: timestamppb.New(info.ModTime()),
	}

	if base := getBase(info.Sys()); base != nil {
		entry.CreatedTime = toTimestamp(base.Ctim)
		entry.ModifiedTime = toTimestamp(base.Mtim)
		entry.Owner = base.Uid
		entry.Group = base.Gid
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
