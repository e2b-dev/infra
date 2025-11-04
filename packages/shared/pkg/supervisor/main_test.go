package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ErrSignal = errors.New("oh noes")

func TestHappyPath(t *testing.T) {
	t.Run("context cancellation will terminate all tasks", func(t *testing.T) {
		// setup
		var counter int
		var cleanup bool
		var tasks []Task
		tasks = append(tasks, Task{
			Name: "run something in the background",
			Cleanup: func(context.Context) error {
				cleanup = true

				return nil
			},
			Background: func(ctx context.Context) error {
				ticker := time.Tick(200 * time.Millisecond)

				for {
					select {
					case <-ticker:
						counter++
					case <-ctx.Done():
						return ErrSignal
					}
				}
			},
		})

		// run tasks for 500 ms
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		t.Cleanup(cancel)

		err := Run(ctx, Options{Tasks: tasks})
		require.ErrorIs(t, err, context.DeadlineExceeded)

		// verify that the task only ran twice
		assert.Equal(t, 2, counter)

		// verify that the cleanup function was called
		assert.True(t, cleanup)
	})

	t.Run("exited task will cancel the rest of the tasks", func(t *testing.T) {
		var tasks []Task

		// setup tasks
		tasks = append(tasks, Task{
			Name: "task which will exit",
			Background: func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
					return nil
				}
			},
		})

		var task, cleanup bool
		tasks = append(tasks, Task{
			Name: "task which will not exit",
			Background: func(ctx context.Context) error {
				defer func() { task = true }()

				<-ctx.Done()

				return nil
			},
			Cleanup: func(context.Context) error {
				cleanup = true

				return nil
			},
		})

		// run for no longer than 500ms
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		t.Cleanup(cancel)

		err := Run(ctx, Options{Tasks: tasks})
		var tee TaskExitedError
		require.ErrorAs(t, err, &tee)
		assert.Equal(t, "task which will exit", tee.TaskName)
		require.NoError(t, tee.TaskError)
		assert.True(t, task)
		assert.True(t, cleanup)
	})

	t.Run("context cancelled before closing", func(t *testing.T) {
		tasks := []Task{
			{
				Name: "clean up requires valid context",
				Cleanup: func(ctx context.Context) error {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
						return ErrSignal
					}
				},
			},
		}

		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		t.Cleanup(cancel)

		err := Run(ctx, Options{Tasks: tasks})
		require.ErrorIs(t, err, ErrSignal)
	})
}
