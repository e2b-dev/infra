package rootfs

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"text/template"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

func generateFile(t *template.Template, model *templateModel) ([]byte, error) {
	var buff bytes.Buffer
	if err := t.Execute(&buff, &model); err != nil {
		return nil, fmt.Errorf("error executing template %q: %w", t.Name(), err)
	}
	if len(model.paths) == 0 {
		return nil, fmt.Errorf("template %q did not set path", t.Name())
	}

	data := bytes.TrimSpace(buff.Bytes())

	return data, nil
}

type templateModel struct {
	Context buildcontext.BuildContext

	Hostname            string
	ProvisionLogPrefix  string
	ProvisionResultPath string
	ProvisionExitPrefix string
	Nameserver          string

	paths []struct {
		path string
		mode int64
	}
}

func newTemplateModel(buildContext buildcontext.BuildContext, provisionLogPrefix, provisionResultPath string) *templateModel {
	return &templateModel{
		Context:             buildContext,
		Hostname:            "e2b.local",
		ProvisionLogPrefix:  provisionLogPrefix,
		ProvisionExitPrefix: ProvisioningExitPrefix,
		ProvisionResultPath: provisionResultPath,
		Nameserver:          sandbox_network.DefaultNameserver,
	}
}

func (t *templateModel) MemoryLimit() int {
	return int(math.Min(float64(t.Context.Config.MemoryMB)/2, 512))
}

func (t *templateModel) WriteFile(path string, mode int64) string {
	t.paths = append(t.paths, struct {
		path string
		mode int64
	}{
		path: strings.TrimPrefix(path, "/"),
		mode: mode,
	})

	return "" // no real return value
}
