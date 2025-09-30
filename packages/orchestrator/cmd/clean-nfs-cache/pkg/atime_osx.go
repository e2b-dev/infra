//go:build darwin

package pkg

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

func GetFileMetadata(path string) (File, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return File{}, fmt.Errorf("could not stat info: %w", err)
	}

	actualStruct, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return File{}, fmt.Errorf("stat did not return a syscall.Stat_t for %q: %T",
			stat.Name(), stat.Sys())
	}

	return File{
		Path:  stat.Name(),
		Size:  stat.Size(),
		ATime: fromTimespec(actualStruct.Atimespec),
		BTime: fromTimespec(actualStruct.Birthtimespec),
	}, nil
}

func fromTimespec(ts syscall.Timespec) time.Time {
	return time.Unix(ts.Sec, ts.Nsec)
}
