package block

type block struct {
	start int64
	end   int64
}

func listBlocks(start, end, blockSize int64) []block {
	blocks := make([]block, (end-start+blockSize-1)/blockSize)

	for i := range blocks {
		blocks[i] = block{
			start: start + int64(i)*blockSize,
			end:   start + int64(i+1)*blockSize,
		}
	}

	return blocks
}
