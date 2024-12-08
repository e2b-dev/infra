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

func BlockIdx(off, blockSize int64) int64 {
	return off / blockSize
}

func BlockOffset(idx, blockSize int64) int64 {
	return idx * blockSize
}
