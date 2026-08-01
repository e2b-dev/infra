// No build tag on purpose — provision_test.go must run on darwin too.
package base

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	tt "text/template"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/phases/base/distro"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

//go:embed provision.sh
var provisionScriptFile string

var ProvisionScriptTemplate = tt.Must(tt.New("provisioning-script").Parse(provisionScriptFile))

type ProvisionScriptParams struct {
	BusyBox    string
	ResultPath string
	Provider   string
	// Distro is the profile registry's contribution to the script: the
	// selection case arms, the rejected-id pattern and the supported-id list.
	Distro distro.TemplateData
}

func getProvisionScript(
	ctx context.Context,
	params ProvisionScriptParams,
) (string, error) {
	var scriptDef bytes.Buffer
	err := ProvisionScriptTemplate.Execute(&scriptDef, params)
	if err != nil {
		return "", fmt.Errorf("error executing provision script: %w", err)
	}
	telemetry.ReportEvent(ctx, "executed provision script env")

	return scriptDef.String(), nil
}
