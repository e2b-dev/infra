package storage

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOffsetReader_Read(t *testing.T) {
	t.Parallel()

	data := []byte("hello world")
	readerAt := bytes.NewReader(data)

	tests := []struct {
		name           string
		offset         int64
		readSize       int
		expectedData   string
		expectedN      int
		expectedErr    error
		expectedOffset int64
	}{
		{
			name:           "read from start",
			offset:         0,
			readSize:       5,
			expectedData:   "hello",
			expectedN:      5,
			expectedErr:    nil,
			expectedOffset: 5,
		},
		{
			name:           "read from offset",
			offset:         6,
			readSize:       5,
			expectedData:   "world",
			expectedN:      5,
			expectedErr:    nil,
			expectedOffset: 11,
		},
		{
			name:           "read until EOF",
			offset:         0,
			readSize:       11,
			expectedData:   "hello world",
			expectedN:      11,
			expectedErr:    nil,
			expectedOffset: 11,
		},
		{
			name:           "read past EOF",
			offset:         0,
			readSize:       15,
			expectedData:   "hello world",
			expectedN:      11,
			expectedErr:    io.EOF,
			expectedOffset: 11,
		},
		{
			name:           "read exactly at EOF",
			offset:         11,
			readSize:       5,
			expectedData:   "",
			expectedN:      0,
			expectedErr:    io.EOF,
			expectedOffset: 11,
		},
		{
			name:           "read zero bytes",
			offset:         0,
			readSize:       0,
			expectedData:   "",
			expectedN:      0,
			expectedErr:    nil,
			expectedOffset: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newOffsetReader(readerAt, tt.offset)
			p := make([]byte, tt.readSize)
			n, err := r.Read(p)

			require.ErrorIs(t, err, tt.expectedErr)
			assert.Equal(t, tt.expectedN, n)
			assert.Equal(t, tt.expectedData, string(p[:n]))
			assert.Equal(t, tt.expectedOffset, r.offset)
		})
	}
}

func TestOffsetReader_SequentialReads(t *testing.T) {
	t.Parallel()

	data := []byte("hello world")
	readerAt := bytes.NewReader(data)
	r := newOffsetReader(readerAt, 0)

	// First read
	p1 := make([]byte, 6)
	n1, err1 := r.Read(p1)
	require.NoError(t, err1)
	assert.Equal(t, 6, n1)
	assert.Equal(t, "hello ", string(p1[:n1]))
	assert.Equal(t, int64(6), r.offset)

	// Second read
	p2 := make([]byte, 5)
	n2, err2 := r.Read(p2)
	require.NoError(t, err2)
	assert.Equal(t, 5, n2)
	assert.Equal(t, "world", string(p2[:n2]))
	assert.Equal(t, int64(11), r.offset)

	// Third read (EOF)
	p3 := make([]byte, 5)
	n3, err3 := r.Read(p3)
	require.ErrorIs(t, err3, io.EOF)
	assert.Equal(t, 0, n3)
	assert.Equal(t, int64(11), r.offset)
}
