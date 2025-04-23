package system

import (
	"math/rand"
	"testing"
)

// TestShouldFail should fail in 50% of the cases to test flaky tests handling
func TestShouldFail(t *testing.T) {
	shouldFail := false
	if rand.Intn(2) == 0 {
		shouldFail = true
	}

	if shouldFail {
		t.Fatal("This test failed intentionally")
	} else {
		t.Log("This test passed intentionally")
	}
}
