package block

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const (
	SEEK_DATA = 3
	SEEK_HOLE = 4
)

type SparseFile struct {
	file *os.File
}

// We can potentially use fibmap for checking if the block is empty (https://github.com/frostschutz/go-fibmap)
func NewSparseFileChecker(f *os.File) *SparseFile {
	return &SparseFile{
		file: f,
	}
}

// [a,b)
func (s *SparseFile) FirstMarked(off int64) (int64, error) {
	start, err := seekData(s.file, off)
	if err != nil {
		return 0, err
	}

	return start, nil
}

func (s *SparseFile) FirstUnmarked(offset int64) (int64, error) {
	start, err := seekHole(s.file, offset)
	if err != nil {
		return 0, err
	}

	return start, nil
}

func seekData(file *os.File, offset int64) (start int64, err error) {
	var syserr syscall.Errno

	start, err = file.Seek(offset, SEEK_DATA)
	if errors.As(err, &syserr) {
		if syserr == syscall.ENXIO {
			return 0, io.EOF
		}
		return 0, fmt.Errorf("error seeking to data: %w", err)
	}

	if err != nil {
		return 0, fmt.Errorf("error seeking to data: %w", err)
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
