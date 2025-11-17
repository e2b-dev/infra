package worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Pool manages a pool of workers for parallel execution
type Pool struct {
	workers     int
	tasks       chan task
	wg          sync.WaitGroup
	logger      *zap.Logger
	once        sync.Once
	started     bool
	startMutex  sync.Mutex
	pendingWg   sync.WaitGroup // WaitGroup for pending tasks
}

// task represents a task to be executed
type task struct {
	ctx      context.Context
	fn       func(context.Context) (interface{}, error)
	callback func(Result)
}

// Result represents the result of a task execution
type Result struct {
	Data  interface{}
	Error error
}

// NewPool creates a new worker pool
func NewPool(workers int, logger *zap.Logger) *Pool {
	if workers <= 0 {
		workers = 10 // Default to 10 workers
	}

	pool := &Pool{
		workers: workers,
		tasks:   make(chan task, workers*2), // Buffer to allow some queuing
		logger:  logger,
	}

	return pool
}

// start initializes and starts the worker pool
func (p *Pool) start() {
	p.startMutex.Lock()
	defer p.startMutex.Unlock()

	if p.started {
		return
	}

	p.started = true

	// Start worker goroutines
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// worker is a worker goroutine that processes tasks
func (p *Pool) worker(id int) {
	defer p.wg.Done()

	for task := range p.tasks {
		// Check if context is already cancelled
		if task.ctx.Err() != nil {
			if task.callback != nil {
				task.callback(Result{
					Data:  nil,
					Error: task.ctx.Err(),
				})
			}
			continue
		}

		// Execute the task
		result := Result{}
		result.Data, result.Error = task.fn(task.ctx)

		// Call callback if provided
		if task.callback != nil {
			task.callback(result)
		}
	}
}

// Execute executes a task synchronously and returns the result
func (p *Pool) Execute(ctx context.Context, fn func(context.Context) (interface{}, error)) Result {
	p.once.Do(p.start)

	// Create a channel to receive the result
	resultChan := make(chan Result, 1)

	// Submit task
	select {
	case p.tasks <- task{
		ctx: ctx,
		fn:  fn,
		callback: func(result Result) {
			resultChan <- result
		},
	}:
		// Task submitted successfully
	case <-ctx.Done():
		return Result{
			Data:  nil,
			Error: ctx.Err(),
		}
	case <-time.After(5 * time.Second):
		// Timeout waiting to submit task (pool might be full)
		return Result{
			Data:  nil,
			Error: context.DeadlineExceeded,
		}
	}

	// Wait for result
	select {
	case result := <-resultChan:
		return result
	case <-ctx.Done():
		return Result{
			Data:  nil,
			Error: ctx.Err(),
		}
	}
}

// ExecuteAsync executes a task asynchronously with a callback
func (p *Pool) ExecuteAsync(ctx context.Context, fn func(context.Context) (interface{}, error), callback func(Result)) {
	p.once.Do(p.start)
	p.pendingWg.Add(1)

	select {
	case p.tasks <- task{
		ctx:      ctx,
		fn:       fn,
		callback: func(result Result) {
			defer p.pendingWg.Done()
			if callback != nil {
				callback(result)
			}
		},
	}:
		// Task submitted successfully
	case <-ctx.Done():
		p.pendingWg.Done()
		if callback != nil {
			callback(Result{
				Data:  nil,
				Error: ctx.Err(),
			})
		}
	case <-time.After(5 * time.Second):
		// Timeout waiting to submit task (pool might be full)
		p.pendingWg.Done()
		if callback != nil {
			callback(Result{
				Data:  nil,
				Error: context.DeadlineExceeded,
			})
		}
	}
}

// Wait waits for all pending tasks to complete
func (p *Pool) Wait() {
	p.pendingWg.Wait()
}

