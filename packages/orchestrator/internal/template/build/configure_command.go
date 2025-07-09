package build

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	tt "text/template"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
)

const configurationTimeout = 5 * time.Minute

//go:embed configure.sh
var configureScriptFile string
var ConfigureScriptTemplate = tt.Must(tt.New("provisioning-finish-script").Parse(configureScriptFile))

func runConfiguration(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	logger *zap.Logger,
	postProcessor *writer.PostProcessor,
	sandboxID string,
) error {
	configCtx, configCancel := context.WithTimeout(ctx, configurationTimeout)
	defer configCancel()

	// Run configuration script
	var scriptDef bytes.Buffer
	err := ConfigureScriptTemplate.Execute(&scriptDef, map[string]string{})
	if err != nil {
		return fmt.Errorf("error executing provision script: %w", err)
	}

	err = sandboxtools.RunCommand(
		configCtx,
		tracer,
		proxy,
		logger,
		postProcessor,
		"config",
		sandboxID,
		scriptDef.String(),
		"root",
		nil,
		map[string]string{},
	)
	if err != nil {
		return fmt.Errorf("error running configuration script: %w", err)
	}

	return nil
}
