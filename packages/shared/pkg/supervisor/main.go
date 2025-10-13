package supervisor

import "context"

type Runner struct{}

func New() *Runner {
	return &Runner{}
}

func (s *Runner) Run(ctx context.Context) error {
	panic("implement me")
}

func (s *Runner) Close(ctx context.Context) error {
	panic("implement me")
}

func (s *Runner) AddTask(name string, options ...Option) {
	var opt Options
	for _, o := range options {
		o(&opt)
	}

	panic("implement me")
}

func WithCleanup(fn func(context.Context) error) Option {
	panic("implement me")
}

func WithBackgroundJob(fn func(context.Context) error) Option {
	panic("implement me")
}

type Options struct{}

type Option func(*Options)
