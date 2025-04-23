package system

import (
	"testing"
)

var shouldFail = true

// TestShouldFail should fail in 50% of the cases to test flaky tests handling
func TestShouldFail(t *testing.T) {
	if shouldFail {
		t.Fatal("This test failed intentionally")
		shouldFail = false
	} else {
		t.Log("This test passed intentionally")
	}
}
