package testutils

func repeatToSize(src []byte, size uint64) []byte {
	if len(src) == 0 || size <= 0 {
		return nil
	}

	dst := make([]byte, size)
	for i := uint64(0); i < size; i += uint64(len(src)) {
		end := i + uint64(len(src))
		if end > size {
			end = size
		}
		copy(dst[i:end], src[:end-i])
	}

	return dst
}

func PrepareTestData(pagesize, pagesInTestData uint64) (data *mockSlicer, size uint64) {
	size = pagesize * pagesInTestData

	data = newMockSlicer(
		repeatToSize(
			[]byte("Hello from userfaultfd! This is our test content that should be readable after the page fault."),
			size,
		),
	)

	return data, size
}
