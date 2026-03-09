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
	UseSystemdOOMD      bool

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

const (
	megabyte     = 1024 * 1024
	maxReserveKB = 128 * 1024 // 128 MB in KB
)

// TotalMemoryBytes returns the total VM memory in bytes.
func (t *templateModel) TotalMemoryBytes() int64 {
	return t.Context.Config.MemoryMB * int64(megabyte)
}

// reservedBytes returns the memory reserved for system services (envd, socat, etc.).
// At most 1/8 of total memory, capped at 128 MB.
func (t *templateModel) reservedBytes() int64 {
	total := t.TotalMemoryBytes()
	eighth := total / 8

	cap := int64(128) * int64(megabyte)
	if eighth < cap {
		return eighth
	}

	return cap
}

// UserMemoryMaxBytes returns the hard memory ceiling for user processes.
func (t *templateModel) UserMemoryMaxBytes() int64 {
	return t.TotalMemoryBytes() - t.reservedBytes()
}

// UserMemoryHighBytes returns the throttling threshold (90% of max) for user processes.
func (t *templateModel) UserMemoryHighBytes() int64 {
	return t.UserMemoryMaxBytes() * 9 / 10
}

// PtyMemoryMaxBytes returns the hard memory ceiling for PTY processes.
// More generous than user -- killing interactive sessions is more disruptive.
func (t *templateModel) PtyMemoryMaxBytes() int64 {
	return t.TotalMemoryBytes() - t.reservedBytes()/2
}

// PtyMemoryHighBytes returns the throttling threshold (90% of max) for PTY processes.
func (t *templateModel) PtyMemoryHighBytes() int64 {
	return t.PtyMemoryMaxBytes() * 9 / 10
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
