package block

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const SEEK_HOLE = 4

type SparseFileMarker struct {
	file *os.File
}

// We can potentially use fibmap for checking if the block is empty (https://github.com/frostschutz/go-fibmap)
func NewSparseFileMarker(f *os.File) *SparseFileMarker {
	return &SparseFileMarker{
		file: f,
	}
}

func (s *SparseFileMarker) FirstUnmarked(offset int64) (int64, error) {
	start, err := seekHole(s.file, offset)
	if err != nil {
		return 0, err
	}

	return start, nil
}

func seekHole(file *os.File, offset int64) (start int64, err error) {
	var syserr syscall.Errno

	start, err = file.Seek(offset, SEEK_HOLE)
	if errors.As(err, &syserr) {
		if syserr == syscall.ENXIO {
			return 0, io.EOF
		}
		return 0, fmt.Errorf("error seeking to hole: %w", err)
	}

	if err != nil {
		return 0, fmt.Errorf("error seeking to hole: %w", err)
	}

	return start, err
}
