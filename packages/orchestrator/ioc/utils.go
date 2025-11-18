package ioc

import (
	"go.uber.org/fx"
)

type IfBuilder struct {
	condition  bool
	moduleName string
	trueOpts   []fx.Option
	falseOpts  []fx.Option
}

func If(moduleName string, cond bool, opts ...fx.Option) IfBuilder {
	return IfBuilder{condition: cond, moduleName: moduleName, trueOpts: opts}
}

func (i IfBuilder) Else(opts ...fx.Option) IfBuilder {
	i.falseOpts = append(i.falseOpts, opts...)

	return i
}

func (i IfBuilder) Build() fx.Option {
	if i.condition {
		return fx.Module(i.moduleName, i.trueOpts...)
	}

	return fx.Module(i.moduleName, i.falseOpts...)
}

func invokeAsync(s fx.Shutdowner, fn func() error) {
	go func() {
		if err := fn(); err != nil {
			s.Shutdown(fx.ExitCode(1))
		}
	}()
}
