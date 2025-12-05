{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/hosts" 0o644 }}

127.0.0.1	localhost
::1	        localhost ip6-localhost ip6-loopback
fe00::	    ip6-localnet
ff00::	    ip6-mcastprefix
ff02::1	    ip6-allnodes
ff02::2	    ip6-allrouters
127.0.1.1	{{ .Hostname }}