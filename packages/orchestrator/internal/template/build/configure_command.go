package build

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	tt "text/template"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const configurationTimeout = 5 * time.Minute

//go:embed configure.sh
var configureScriptFile string
var ConfigureScriptTemplate = tt.Must(tt.New("provisioning-finish-script").Parse(configureScriptFile))

type ConfigurationParams struct {
	EnvID      string
	TemplateID string
	BuildID    string
}

func runConfiguration(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	postProcessor *writer.PostProcessor,
	metadata storage.TemplateFiles,
	sandboxID string,
) error {
	configCtx, configCancel := context.WithTimeout(ctx, configurationTimeout)
	defer configCancel()

	// Run configuration script
	var scriptDef bytes.Buffer
	err := ConfigureScriptTemplate.Execute(&scriptDef, ConfigurationParams{
		EnvID:      metadata.TemplateID,
		TemplateID: metadata.TemplateID,
		BuildID:    metadata.BuildID,
	})
	if err != nil {
		return fmt.Errorf("error executing provision script: %w", err)
	}

	err = sandboxtools.RunCommand(
		configCtx,
		tracer,
		proxy,
		postProcessor,
		zapcore.DebugLevel,
		"config",
		sandboxID,
		scriptDef.String(),
		sandboxtools.CommandMetadata{
			User: "root",
		},
	)
	if err != nil {
		return fmt.Errorf("error running configuration script: %w", err)
	}

	return nil
}
