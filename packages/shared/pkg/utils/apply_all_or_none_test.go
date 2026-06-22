package utils

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApplyAllOrNoneRollbackIgnoresParentCancelButPreservesDeadline(t *testing.T) {
	t.Parallel()

	failErr := errors.New("fail")
	deadline := time.Now().Add(time.Hour)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	cancel()
	rollbackCalled := false

	err := ApplyAllOrNone(ctx, []UpdateFunc{
		func(context.Context) (func(context.Context), error) {
			return func(ctx context.Context) {
				rollbackCalled = true

				rollbackDeadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.True(t, rollbackDeadline.Equal(deadline))
				require.NoError(t, ctx.Err())
			}, nil
		},
		func(context.Context) (func(context.Context), error) {
			return nil, failErr
		},
	})

	require.ErrorIs(t, err, failErr)
	require.True(t, rollbackCalled)
}
