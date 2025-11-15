package ioc

import "go.uber.org/fx"

func If(moduleName string, cond bool, a ...fx.Option) fx.Option {
	if !cond {
		return fx.Module(moduleName)
	}

	return fx.Module(moduleName, a...)
}
