package cleaner

import (
	"testing"
	"unsafe"
)

// sizeOfDir returns an estimate of the memory used by a Dir value
// including the name bytes (the string header is counted in unsafe.Sizeof).
func sizeOfDir(d *Dir) uintptr {
	sz := unsafe.Sizeof(*d)
	sz += uintptr(len(d.Name))

	return sz
}

// sizeOfFile returns an estimate of the memory used by a File value
// including the name bytes.
func sizeOfFile(f *File) uintptr {
	sz := unsafe.Sizeof(*f)
	sz += uintptr(len(f.Name))

	return sz
}

func BenchmarkSizes(b *testing.B) {
	// create a 64-byte name
	nameBytes := make([]byte, 64)
	for i := range nameBytes {
		nameBytes[i] = 'x'
	}
	name := string(nameBytes)

	d := &Dir{Name: name}
	f := &File{Name: name}

	b.Run("DirSizeBytes", func(b *testing.B) {
		for range b.N {
			_ = sizeOfDir(d)
		}
		b.ReportMetric(float64(sizeOfDir(d)), "bytes")
		b.Logf("Dir: struct header %d bytes + name %d bytes = %d bytes", unsafe.Sizeof(*d), len(d.Name), sizeOfDir(d))
	})

	b.Run("FileSizeBytes", func(b *testing.B) {
		for range b.N {
			_ = sizeOfFile(f)
		}
		b.ReportMetric(float64(sizeOfFile(f)), "bytes")
		b.Logf("File: struct header %d bytes + name %d bytes = %d bytes", unsafe.Sizeof(*f), len(f.Name), sizeOfFile(f))
	})
}
