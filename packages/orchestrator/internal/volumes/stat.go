package volumes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) Stat(_ context.Context, request *orchestrator.StatRequest) (r *orchestrator.StatResponse, err error) {
	defer func() {
		err = v.processError(err)
	}()

	fullPath, err := v.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, fmt.Errorf("failed to build volume path: %w", err)
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat path: %w", err)
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
		ModifiedTime: timestamppb.New(info.ModTime()),
	}

	if base := getBase(info.Sys()); base != nil {
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
