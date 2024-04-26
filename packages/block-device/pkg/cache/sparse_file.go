package cache

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
)

const SEEK_HOLE = 4

type ErrEndOfFile struct{}

func (ErrEndOfFile) Error() string {
	return "End of file."
}

type ErrNoMarkFound struct{}

func (ErrNoMarkFound) Error() string {
	return "No mark found."
}

type SparseFileView struct {
	file *os.File
}

func NewSparseFileView(f *os.File) *SparseFileView {
	return &SparseFileView{
		file: f,
	}
}

// Returns first marked block range for the given offset.
// If the current block is not marked it will return ErrNoMarkFound.
// This function stops searching for marked block when it encounters first unmarked block.
// If the offset starts in a market block the range will show in what block does the continuous marking ends.
func (s *SparseFileView) MarkedBlockRange(offset int64) (start int64, end int64, err error) {
	firstUnmarked, err := s.firstUnmarked(offset)
	if errors.As(err, &ErrEndOfFile{}) {
		return 0, 0, ErrNoMarkFound{}
	}

	if err != nil {
		return 0, 0, err
	}

	if firstUnmarked == offset {
		return 0, 0, ErrNoMarkFound{}
	}

	start = offset / block.Size
	end = (firstUnmarked + block.Size) / block.Size

	return start * block.Size, end*block.Size - 1, nil
}

func (s *SparseFileView) firstUnmarked(offset int64) (int64, error) {
	start, err := seekHole(s.file, offset)
	if err != nil {
		return 0, err
	}

	return start, nil
}

func seekHole(file *os.File, offset int64) (int64, error) {
	var syserr syscall.Errno

	start, err := file.Seek(offset, SEEK_HOLE)
	if errors.As(err, &syserr) {
		if syserr == syscall.ENXIO {
			return 0, ErrEndOfFile{}
		}

		return 0, fmt.Errorf("error seeking data: %w", err)
	}

	if err != nil {
		return 0, fmt.Errorf("error seeking data: %w", err)
	}

	return start, nil
}
