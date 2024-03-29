package main

import (
	"flag"
	"net/http"
	_ "net/http/pprof"

	"github.com/e2b-dev/infra/packages/env-build-task-driver/internal/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	log "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/plugins"

	driver "github.com/e2b-dev/infra/packages/env-build-task-driver/internal"
)

const (
	profilingPort = ":6062"
)

func factory(log log.Logger) interface{} {
	return driver.NewPlugin(log)
}

func main() {
	// Create pprof endpoint for profiling
	go func() {
		http.ListenAndServe(profilingPort, nil)
	}()

	envID := flag.String("env", "", "env id")
	buildID := flag.String("build", "", "build id")

	flag.Parse()

	if *envID != "" && *buildID != "" {
		// Start of mock build for testing
		env.MockBuild(*envID, *buildID)
		return
	} else {
		shutdown := telemetry.InitOTLPExporter(driver.PluginName, driver.PluginVersion)
		defer shutdown()
	}

	plugins.Serve(factory)
}
