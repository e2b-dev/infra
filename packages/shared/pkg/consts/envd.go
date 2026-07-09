package consts

const (
	DefaultEnvdServerPort int64 = 49983

	// SystemTag opts a process into envd's root cgroup (no user/pty/socat).
	// Used for maintenance commands that must outlive cgroup freezing.
	SystemTag = "_system"
)
