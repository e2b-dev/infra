package memory

import (
	"bytes"
	"os"
	"testing"
)

func TestMemoryView(t *testing.T) {
	pagesize := uint64(4096)
	data, size := PrepareTestData(pagesize)

	memoryArea, memoryStart := NewMock4KPageMmap(size)

	n := copy(memoryArea[0:size], data.content)

	if n != int(size) {
		t.Fatal("failed to copy data", n)
	}

	m := NewContiguousMap(memoryStart, size, pagesize)

	view, err := NewView(os.Getpid(), m)
	if err != nil {
		t.Fatal("failed to create view", err)
	}
	defer view.Close()

	for i := 0; i < int(size); i += int(pagesize) {
		buf := make([]byte, pagesize)
		view.ReadAt(buf, int64(i))

		if !bytes.Equal(buf, data.content[i:i+int(pagesize)]) {
			t.Fatal("failed to read data", buf)
		}
	}
}
