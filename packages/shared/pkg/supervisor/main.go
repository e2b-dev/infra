package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"sync"
	"syscall"
)

type Runner struct {
	tasks []Task

	mu          sync.Mutex
	started     bool
	closed      bool
	doneCh      chan taskResult
	wg          sync.WaitGroup
	cancelFunc  context.CancelFunc
	cleanupsRun bool
}

func New() *Runner {
	return &Runner{}
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
	for _, task := range s.tasks {
		if task.Background != nil {
			s.startTask(runCtx, task)
		}
	}

	// Setup OS signal listener
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Wait for a stop condition
	var reason error
	select {
	case <-ctx.Done():
		reason = ctx.Err()
	case <-sigCtx.Done():
		reason = context.Canceled
	case res := <-s.doneCh:
		// A task finished (with or without error)
		if res.err == nil {
			reason = fmt.Errorf("%s exited successfully", res.name)
		} else {
			reason = fmt.Errorf("%s exited with %w", res.name, res.err)
		}
	}

	// Cancel all tasks and wait for them to finish
	cancel()
	s.wg.Wait()

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

	s.wg.Add(1)
	go func(t Task) {
		defer s.wg.Done()

		err := t.Background(ctx)
		// Non-blocking send in case Run already finished
		select {
		case s.doneCh <- taskResult{name: t.Name, err: err}:
		default:
		}
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
	// Wait for tasks to exit
	s.wg.Wait()

	// run cleanups in reverse order
	var errs []error
	for index := len(s.tasks) - 1; index >= 0; index-- {
		task := s.tasks[index]
		if task.Cleanup != nil {
			if err := task.Cleanup(ctx); err != nil {
				errs = append(errs, err)
			}
		}
	}

	s.mu.Lock()
	s.cleanupsRun = true
	s.closed = true
	s.mu.Unlock()

	return errors.Join(errs...)
}

func (s *Runner) AddTask(name string, options ...Option) {
	task := Task{Name: name}
	for _, o := range options {
		o(&task)
	}

	s.Register(task)
}

func (s *Runner) Register(task Task) {
	s.tasks = append(s.tasks, task)
}

func WithCleanup(fn func(context.Context) error) Option {
	return func(t *Task) {
		t.Cleanup = fn
	}
}

func WithBackgroundJob(fn func(context.Context) error) Option {
	return func(t *Task) {
		t.Background = fn
	}
}

type Task struct {
	Name       string
	Background func(context.Context) error
	Cleanup    func(context.Context) error
}

type Option func(*Task)
