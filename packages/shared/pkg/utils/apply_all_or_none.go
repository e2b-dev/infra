package utils

import "context"

// UpdateFunc performs an update and returns a rollback function to revert it.
// The rollback receives a context that is guaranteed not to be canceled.
type UpdateFunc = func(ctx context.Context) (rollback func(ctx context.Context), err error)

// ApplyAllOrNone applies updates sequentially. If any update fails,
// already-applied updates are rolled back in reverse order. Rollbacks receive
// a non-cancelable context to ensure they complete even if the original
// request context has been canceled.
func ApplyAllOrNone(ctx context.Context, updates []UpdateFunc) error {
	var rollbacks []func(ctx context.Context)

	for _, update := range updates {
		rollback, err := update(ctx)
		if err != nil {
			rollbackCtx := context.WithoutCancel(ctx)
			for i := len(rollbacks) - 1; i >= 0; i-- {
				rollbacks[i](rollbackCtx)
			}

			return err
		}

		if rollback != nil {
			rollbacks = append(rollbacks, rollback)
		}
	}

	return nil
}
