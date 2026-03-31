package supabaseauthusersync

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	defaultRestartDelay      = time.Second
	maxRestartDelay          = 30 * time.Second
	healthyRunResetThreshold = time.Minute
)

type supervisorConfig struct {
	RestartDelay         time.Duration
	MaxRestartDelay      time.Duration
	HealthyRunResetAfter time.Duration
}

func defaultSupervisorConfig() supervisorConfig {
	return supervisorConfig{
		RestartDelay:         defaultRestartDelay,
		MaxRestartDelay:      maxRestartDelay,
		HealthyRunResetAfter: healthyRunResetThreshold,
	}
}

func (r *Runner) RunWithRestart(ctx context.Context) error {
	return supervise(ctx, r.l, defaultSupervisorConfig(), r.Run)
}

func supervise(ctx context.Context, l logger.Logger, cfg supervisorConfig, run func(context.Context) error) error {
	restartAttempt := 0

	for {
		startedAt := time.Now()
		err := runRecovering(ctx, l, run)
		runtime := time.Since(startedAt)

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if runtime >= cfg.HealthyRunResetAfter {
			restartAttempt = 0
		}
		restartAttempt++

		delay := restartBackoff(restartAttempt, cfg.RestartDelay, cfg.MaxRestartDelay)
		l.Error(ctx, "supabase auth user sync worker exited unexpectedly; restarting",
			zap.Error(err),
			zap.Int("worker.restart_attempt", restartAttempt),
			zap.Duration("worker.restart_in", delay),
			zap.Duration("worker.runtime", runtime),
			zap.Duration("worker.healthy_run_reset_after", cfg.HealthyRunResetAfter),
		)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()

			return ctx.Err()
		case <-timer.C:
		}
	}
}

func runRecovering(ctx context.Context, l logger.Logger, run func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			l.Error(ctx, "supabase auth user sync worker panicked",
				zap.String("worker.panic", fmt.Sprint(recovered)),
				zap.String("worker.stack", string(debug.Stack())),
			)

			err = fmt.Errorf("worker panic: %v", recovered)
		}
	}()

	err = run(ctx)
	if err == nil && ctx.Err() == nil {
		return errors.New("worker exited without error")
	}

	return err
}

func restartBackoff(attempt int, base time.Duration, maxDelay time.Duration) time.Duration {
	if base <= 0 {
		base = defaultRestartDelay
	}
	if maxDelay < base {
		maxDelay = base
	}

	delay := base
	for i := 1; i < attempt; i++ {
		if delay >= maxDelay/2 {
			return maxDelay
		}

		delay *= 2
	}

	return delay
}
