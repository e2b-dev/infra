//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestIsRetryableUploadErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"no diff", build.NoDiffError{}, false},
		{"object not exist", storage.ErrObjectNotExist, false},
		{"object not exist wrapped", fmt.Errorf("load: %w", storage.ErrObjectNotExist), false},
		{"parent cancelled", context.Canceled, false},
		{"per-attempt deadline", context.DeadlineExceeded, true},
		{"gcs 503", errors.New("server error (503)"), true},
		{"unknown", errors.New("boom"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.retryable, isRetryableUploadErr(tt.err))
		})
	}
}
