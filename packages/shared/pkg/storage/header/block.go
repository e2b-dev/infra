package header

func TotalBlocks(size, blockSize int64) int64 {
	return (size + blockSize - 1) / blockSize
}

func BlocksOffsets(size, blockSize int64) []int64 {
	offsets := make([]int64, TotalBlocks(size, blockSize))

	for i := range offsets {
		offsets[i] = BlockOffset(int64(i), blockSize)
	}

	return offsets
}

// BlockIdx returns the index of the block containing byte offset off (floor division).
func BlockIdx(off, blockSize int64) int64 {
	return off / blockSize
}

// BlockCeilIdx returns the index of the first block after byte offset off (ceiling division).
func BlockCeilIdx(off, blockSize int64) int64 {
	return (off + blockSize - 1) / blockSize
}

func BlockOffset(idx, blockSize int64) int64 {
	return idx * blockSize
}
