//go:build linux

package finalize

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	tt "text/template"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const configurationTimeout = 5 * time.Minute

//go:embed configure.sh
var configureScriptFile string
var ConfigureScriptTemplate = tt.Must(tt.New("provisioning-finish-script").Parse(configureScriptFile))

// packCertBundleCmd regenerates the system trust store and packs it into a
// single contiguous tar. It is run as the build's last guest step — after all
// build steps, start_cmd, and ready_cmd — so the tar equals the trust store the
// guest would otherwise regenerate at boot (update-ca-certificates merges certs
// dropped under /usr/local/share/ca-certificates even if the user never ran it).
// envd.service seeds /etc/ssl/certs from this tar and skips update-ca-certificates
// on cold boot, trading the regen's scattered rootfs reads for one sequential
// read. -h dereferences the hash-named symlinks so the real cert contents are
// packed instead of links that would still fault the lazily-fetched rootfs.
const packCertBundleCmd = `set -e
if command -v update-ca-certificates >/dev/null 2>&1; then
	update-ca-certificates
fi
mkdir -p /usr/local/share/e2b
tar -C /etc/ssl/certs -chf /usr/local/share/e2b/ssl-certs.tar .
`

// packCertBundle runs packCertBundleCmd in the guest as root.
func packCertBundle(
	ctx context.Context,
	userLogger logger.Logger,
	proxy *proxy.SandboxProxy,
	sandboxID string,
) error {
	ctx, span := tracer.Start(ctx, "pack cert bundle")
	defer span.End()

	err := sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		userLogger,
		zapcore.DebugLevel,
		"certs",
		sandboxID,
		packCertBundleCmd,
		metadata.Context{
			User: "root",
		},
	)
	if err != nil {
		return fmt.Errorf("error packing CA cert bundle: %w", err)
	}

	return nil
}

type ConfigurationParams struct {
	EnvID      string
	TemplateID string
	BuildID    string
}

func runConfiguration(
	ctx context.Context,
	userLogger logger.Logger,
	bc buildcontext.BuildContext,
	proxy *proxy.SandboxProxy,
	sandboxID string,
) error {
	ctx, span := tracer.Start(ctx, "run configuration")
	defer span.End()

	// Run configuration script
	var scriptDef bytes.Buffer
	err := ConfigureScriptTemplate.Execute(&scriptDef, ConfigurationParams{
		EnvID:      bc.Config.TemplateID,
		TemplateID: bc.Config.TemplateID,
		BuildID:    bc.Template.BuildID,
	})
	if err != nil {
		return fmt.Errorf("error executing provision script: %w", err)
	}

	err = sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		userLogger,
		zapcore.DebugLevel,
		"config",
		sandboxID,
		scriptDef.String(),
		metadata.Context{
			User: "root",
		},
	)
	if err != nil {
		return fmt.Errorf("error running configuration script: %w", err)
	}

	return nil
}
