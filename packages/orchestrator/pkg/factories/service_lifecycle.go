package factories

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type serviceExit struct {
	name string
	err  error
}

func startManagedService(
	ctx context.Context,
	g *errgroup.Group,
	serviceExited chan<- serviceExit,
	name string,
	l logger.Logger,
	f func() error,
) {
	g.Go(func() error {
		l.Info(ctx, "starting service")

		err := f()
		if err != nil {
			l.Error(ctx, "service returned an error", zap.Error(err))
		} else {
			l.Info(ctx, "service stopped")
		}

		select {
		case serviceExited <- serviceExit{name: name, err: err}:
		default:
			// Don't block if another service exit has already been reported.
		}

		if err != nil {
			return fmt.Errorf("service %s failed: %w", name, err)
		}

		return nil
	})
}
