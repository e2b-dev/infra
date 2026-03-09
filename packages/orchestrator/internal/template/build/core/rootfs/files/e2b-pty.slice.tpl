{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/e2b-pty.slice" 0o644 }}

[Unit]
Description=E2B PTY Processes Slice
Before=slices.target

[Slice]
MemoryHigh={{ .PtyMemoryHighBytes }}
MemoryMax={{ .PtyMemoryMaxBytes }}
ManagedOOMMemoryPressure=kill
ManagedOOMMemoryPressureLimit=90%
CPUWeight=200