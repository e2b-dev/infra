package testutils

import (
	"errors"
	"fmt"
)

// FirstDifferentByte returns the first byte index where a and b differ.
// It also returns the differing byte values (want, got).
// If slices are identical, it returns idx -1.
func ErrorFromByteSlicesDifference(expected, actual []byte) error {
	var errs []error

	if len(expected) > len(actual) {
		errs = append(errs, fmt.Errorf("expected slice (%d bytes) is longer than actual slice (%d bytes)", len(expected), len(actual)))
	} else if len(expected) < len(actual) {
		errs = append(errs, fmt.Errorf("actual slice (%d bytes) is longer than expected slice (%d bytes)", len(actual), len(expected)))
	}

	smallerSize := min(len(expected), len(actual))

	for i := range smallerSize {
		if expected[i] != actual[i] {
			errs = append(errs, fmt.Errorf("first different byte: want '%x', got '%x' at index %d", expected[i], actual[i], i))

			break
		}
	}

	return errors.Join(errs...)
}
