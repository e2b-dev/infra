package ioc

import (
	"go.uber.org/fx"
)

func invokeAsync(s fx.Shutdowner, fn func() error) {
	go func() {
		if err := fn(); err != nil {
			s.Shutdown(fx.ExitCode(1))
		}
	}()
}
