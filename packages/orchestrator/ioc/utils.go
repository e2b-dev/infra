package ioc

import (
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func If(moduleName string, cond bool, a ...fx.Option) fx.Option {
	if !cond {
		return fx.Module(moduleName)
	}

	return fx.Module(moduleName, a...)
}

func invokeAsync(name string, log *zap.Logger, s fx.Shutdowner, fn func() error) {
	go func() {
		if err := fn(); err != nil {
			if err := s.Shutdown(fx.ExitCode(1)); err != nil {
				log.Error("failed to shutdown on async error", zap.Error(err), zap.String("name", name))
			}
		}
	}()
}
