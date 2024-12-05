package header

func NumberOfBlocks(size, blockSize int64) uint {
	return uint((size + blockSize - 1) / blockSize)
}

func ListBlocks(off, size, blockSize int64) []int64 {
	blocks := make([]int64, NumberOfBlocks(size, blockSize))

	for i := range blocks {
		blocks[i] = off + int64(i)*blockSize
	}

	return blocks
}

func GetBlockIdx(off int64, blockSize int64) int64 {
	return off / blockSize
}
