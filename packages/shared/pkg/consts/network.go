package consts

const SandboxDefaultNameserver = "8.8.8.8"

// SandboxDeniedCIDRs are protected from untrusted sandbox egress across every
// runtime backend. Keep runtime-specific firewall representations derived from
// this shared contract.
var SandboxDeniedCIDRs = []string{
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
}
