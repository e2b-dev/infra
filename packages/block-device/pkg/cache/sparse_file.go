package cache

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

const SEEK_DATA = 3

type ErrEndOfFile struct{}

func (ErrEndOfFile) Error() string {
	return "End of file."
}

type SparseFileView struct {
	file *os.File
}

func NewSparseFileView(f *os.File) *SparseFileView {
	return &SparseFileView{
		file: f,
	}
}

func (s *SparseFileView) IsMarked(offset int64) (bool, error) {
	start, err := seekData(s.file, offset)
	if errors.As(err, &ErrEndOfFile{}) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return start == offset, nil
}

func seekData(file *os.File, offset int64) (int64, error) {
	var syserr syscall.Errno

	start, err := file.Seek(offset, SEEK_DATA)
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
