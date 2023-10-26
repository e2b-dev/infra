package main

import (
	"github.com/e2b-dev/infra/packages/shared/utils"
	log "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/plugins"
	"net/http"

	driver "github.com/e2b-dev/infra/packages/env-instance-task-driver/internal"

	_ "net/http/pprof"
)

func configurePlugin() {
	// Create pprof endpoint for profiling
	go func() {
		http.ListenAndServe(":6061", nil)
	}()

	shutdown, err := utils.InitOTLPExporter(driver.PluginName, driver.PluginVersion)
	if err != nil {
		log.Fmt("failed to initialize OTLP exporter: %v", err)
	}
	defer shutdown()

	plugins.Serve(factory)
}

func factory(log log.Logger) interface{} {
	return driver.NewPlugin(log)
}

func main() {
	// Create pprof endpoint for profiling
	go func() {
		http.ListenAndServe(":6062", nil)
	}()

	configurePlugin()
}
