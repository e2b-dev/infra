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

type Runner struct {
	tasks  []Task
	logger *zap.Logger

	mu          sync.Mutex
	started     bool
	closed      bool
	doneCh      chan taskResult
	wg          sync.WaitGroup
	cancelFunc  context.CancelFunc
	cleanupsRun bool
}

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

func New(logger *zap.Logger) *Runner {
	return &Runner{
		logger: logger.Named("supervisor"),
	}
}

func (s *Runner) Run(ctx context.Context) error {
	// Prepare internal context to control all tasks
	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancelFunc = cancel
	if s.doneCh == nil {
		s.doneCh = make(chan taskResult, len(s.tasks))
	}
	s.started = true
	s.mu.Unlock()

	// Start tasks
	s.logger.Info("starting tasks", zap.Int("count", len(s.tasks)))
	for _, task := range s.tasks {
		s.startTask(runCtx, task)
	}

	// Setup OS signal listener
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Wait for a stop condition
	var reason error
	select {
	case <-ctx.Done():
		reason = fmt.Errorf("supervisor context canceled: %w", ctx.Err())
	case sig := <-sigs:
		s.logger.Info("received OS signal", zap.String("signal", sig.String()))
		reason = nil
	case res := <-s.doneCh:
		// A task finished (with or without error)
		reason = TaskExitedError{TaskName: res.name, TaskError: res.err}
	}

	return reason
}

type taskResult struct {
	name string
	err  error
}

func (s *Runner) startTask(ctx context.Context, task Task) {
	if task.Background == nil {
		return
	}

	logger := s.logger.Named(task.Name)

	logger.Info("starting task")
	s.wg.Add(1)
	go func(t Task) {
		defer s.wg.Done()

		err := t.Background(ctx)
		// Non-blocking send in case Run already finished
		select {
		case s.doneCh <- taskResult{name: t.Name, err: err}:
		default:
		}

		logger.Info("task finished", zap.Error(err))
	}(task)
}

func (s *Runner) Close(ctx context.Context) error {
	// Idempotent: if Run already performed cleanup, nothing to do
	s.mu.Lock()
	if s.cleanupsRun {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancelFunc
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// run cleanups in reverse order
	var errs []error
	for index := len(s.tasks) - 1; index >= 0; index-- {
		task := s.tasks[index]
		if task.Cleanup != nil {
			logger := s.logger.Named(task.Name)
			logger.Info("running cleanup")
			if err := task.Cleanup(ctx); err != nil {
				logger.Warn("cleanup failed", zap.Error(err))
				errs = append(errs, err)
			}
		}
	}

	s.wg.Wait()

	s.mu.Lock()
	s.cleanupsRun = true
	s.closed = true
	s.mu.Unlock()

	return errors.Join(errs...)
}

func (s *Runner) AddTask(task Task) {
	if task.Name == "" {
		panic("task name must not be empty")
	}

	s.tasks = append(s.tasks, task)
}

type Task struct {
	Name       string
	Background func(context.Context) error
	Cleanup    func(context.Context) error
}
