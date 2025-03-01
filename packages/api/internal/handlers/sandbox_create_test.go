// test

package handlers

import "testing"

func TestAdd(t *testing.T) {
	result := add(1, 2)
	if result != 3 {
		t.Errorf("expected 3, got %d", result)
	}

}
