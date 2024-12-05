package layer

type Block struct {
	Start int64
	End   int64
}

func ListBlocks(start, end, blockSize int64) []Block {
	blocks := make([]Block, (end-start+blockSize-1)/blockSize)

	for i := range blocks {
		blocks[i] = Block{
			Start: start + int64(i)*blockSize,
			End:   start + int64(i+1)*blockSize,
		}
	}

	return blocks
}
