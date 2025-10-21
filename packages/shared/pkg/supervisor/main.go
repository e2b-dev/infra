package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"go.uber.org/zap"
)

type TaskExitedError struct {
	TaskName  string
	TaskError error
}

func (e TaskExitedError) Unwrap() error {
	return e.TaskError
}

func (e TaskExitedError) Error() string {
	if e.TaskError == nil {
		return fmt.Sprintf("%s exited", e.TaskName)
	}

	return fmt.Sprintf("%s exited with %s", e.TaskName, e.TaskError.Error())
}

type taskResult struct {
	name string
	err  error
}

type Task struct {
	Name       string
	Background func(context.Context) error
	Cleanup    func(context.Context) error
}

type Options struct {
	ForceStop bool
	Tasks     []Task
	Logger    *zap.Logger
}

func Run(ctx context.Context, opts Options) error {
	if opts.Logger == nil {
		opts.Logger = zap.L()
	}

	baseLogger := opts.Logger.Named("supervisor")

	var wg sync.WaitGroup
	doneCh := make(chan taskResult, len(opts.Tasks))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	startTask := func(ctx context.Context, task Task) {
		taskLogger := baseLogger.Named(task.Name)

		if task.Background == nil {
			return
		}

		taskLogger.Info("starting task")
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := task.Background(ctx)
			// Non-blocking send in case Run already finished
			select {
			case doneCh <- taskResult{name: task.Name, err: err}:
			default:
			}

			taskLogger.Info("task finished", zap.Error(err))
		}()
	}

	baseLogger.Info("starting tasks", zap.Int("count", len(opts.Tasks)))
	for _, task := range opts.Tasks {
		startTask(ctx, task)
	}

	// Setup OS signal listener
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	// Wait for a stop condition
	var reason error
	select {
	case <-ctx.Done():
		reason = fmt.Errorf("supervisor context canceled: %w", ctx.Err())
	case sig := <-sigs:
		baseLogger.Info("received OS signal", zap.String("signal", sig.String()))
		reason = nil
	case res := <-doneCh:
		// A task finished (with or without error)
		reason = TaskExitedError{TaskName: res.name, TaskError: res.err}
	}

	if opts.ForceStop {
		cancel()
	}

	var errs []error
	if reason != nil {
		errs = append(errs, reason)
	}

	for index := len(opts.Tasks) - 1; index >= 0; index-- {
		task := opts.Tasks[index]
		taskLogger := baseLogger.Named(task.Name)
		if task.Cleanup != nil {
			taskLogger.Info("running cleanup")
			if err := task.Cleanup(ctx); err != nil {
				taskLogger.Warn("cleanup failed", zap.Error(err))
				errs = append(errs, err)
			}
		}
	}

	wg.Wait()

	return errors.Join(errs...)
}
