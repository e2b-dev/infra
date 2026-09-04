package consts

const (
	DefaultEnvdServerPort int64 = 49983

	// SystemTag opts a process into envd's root cgroup (no user/pty/socat).
	// Used for maintenance commands that must outlive cgroup freezing.
	SystemTag = "_system"

	// TemplateDefaultUser is the user a template build makes envd's default.
	//
	// One definition, because two independent places have to agree on it and the
	// agreement is load-bearing: the finalize phase sends this literal for any build
	// below templates.TemplateV2ReleaseVersion and the recorded Context.User at or
	// above it, so this is the only value BOTH branches produce. A host reconstructing
	// what a build sent can therefore trust exactly this value and nothing else. Two
	// copies that drift would make the reconstruction re-send a user finalize never
	// sent.
	TemplateDefaultUser = "user"
)
