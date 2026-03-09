{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/systemd/system/e2b-user.slice" 0o644 }}

[Unit]
Description=E2B User Processes Slice
Before=slices.target

[Slice]
MemoryHigh={{ .UserMemoryHighBytes }}
MemoryMax={{ .UserMemoryMaxBytes }}
ManagedOOMMemoryPressure=kill
ManagedOOMMemoryPressureLimit=80%
CPUWeight=50