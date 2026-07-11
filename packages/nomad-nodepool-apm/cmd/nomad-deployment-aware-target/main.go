package main

import (
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins"

	targetplugin "github.com/e2b-dev/infra/packages/nomad-nodepool-apm/target"
)

func main() {
	plugins.Serve(factory)
}

func factory(log hclog.Logger) any {
	return targetplugin.New(log)
}
