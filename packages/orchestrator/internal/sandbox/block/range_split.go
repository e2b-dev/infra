package block

// Split ranges so there are no ranges larger than maxSize.
// This is not an optimal split, ideally we would split the ranges so that we can fill each call to unix.ProcessVMReadv to the max size.
// This is though a very simple split that will work and the syscalls overhead here is not very high as opposed to the other things.
func splitOversizedRanges(ranges []Range, maxSize int64) (result []Range) {
	for _, r := range ranges {
		if r.Size <= maxSize {
			result = append(result, r)

			continue
		}

		for offset := int64(0); offset < r.Size; offset += maxSize {
			result = append(result, Range{
				Start: r.Start + offset,
				Size:  min(r.Size-offset, maxSize),
			})
		}
	}

	return result
}
