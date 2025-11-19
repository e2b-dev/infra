package ioc

import (
	"go.uber.org/fx"
)

type caseBuilder struct {
	condition bool
	options   []fx.Option
}

type IfBuilder struct {
	moduleName string
	cases      []caseBuilder
	fallback   []fx.Option
}

func If(moduleName string, cond bool, opts ...fx.Option) IfBuilder {
	return IfBuilder{
		moduleName: moduleName,
		cases: []caseBuilder{
			{condition: cond, options: opts},
		},
	}
}

func (i IfBuilder) ElseIf(cond bool, opts ...fx.Option) IfBuilder {
	i.cases = append(i.cases, caseBuilder{condition: cond, options: opts})

	return i
}

func (i IfBuilder) Else(opts ...fx.Option) IfBuilder {
	i.fallback = opts

	return i
}

func (i IfBuilder) Build() fx.Option {
	for _, item := range i.cases {
		if item.condition {
			return fx.Module(i.moduleName, item.options...)
		}
	}

	return fx.Module(i.moduleName, i.fallback...)
}

func invokeAsync(s fx.Shutdowner, fn func() error) {
	go func() {
		if err := fn(); err != nil {
			s.Shutdown(fx.ExitCode(1))
		}
	}()
}
