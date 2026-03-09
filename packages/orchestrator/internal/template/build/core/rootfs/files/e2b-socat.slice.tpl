{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/e2b-socat.slice" 0o644 }}

[Unit]
Description=E2B Socat Processes Slice
Before=slices.target

[Slice]
MemoryMin=5M
MemoryLow=8M
CPUWeight=150