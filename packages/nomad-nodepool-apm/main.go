package main

import (
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins"

	nodepoolapm "github.com/e2b-dev/infra/packages/nomad-nodepool-apm/plugin"
)

func main() {
	plugins.Serve(factory)
}

// factory returns a new instance of the NodePoolPlugin.
func factory(log hclog.Logger) any {
	return nodepoolapm.NewNodePoolPlugin(log)
}
