package envd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// buildVersionRe constrains a build-envd-version target to a bare version-ish
// identifier (v0.7.0, v0.8.0-rc1, a git SHA). Dots and hyphens are admitted,
// but no path separators and no leading dot, so the value cannot traverse out
// of the envd staging directory when joined into a candidate path.
var buildVersionRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`)

// ResolveBuildBinary returns the host path of the envd binary a template
// build bakes into the rootfs, given the build-envd-version flag's value.
//
// "promoted" (the flag's compiled default) or empty keeps hostEnvdPath — the
// node's promoted binary, the behavior every build has always had.
//
// A concrete version id resolves to a binary staged next to the promoted one,
// in either layout: the flat versioned sibling "envd.<version>" or the
// release-bucket layout "<version>/envd". When neither is staged, the
// promoted binary is accepted iff its baked version already equals the target
// (leading "v" normalized) — on central-mount nodes /fc-envd is a read-only
// --only-dir view of exactly one release, so the promoted binary is the only
// binary there and pinning the version the node already runs must be a no-op,
// not an outage. Anything else FAILS the build: feature gates key on the envd
// version a build bakes, so silently substituting a different binary would
// misgate features for every sandbox created from the template.
func ResolveBuildBinary(ctx context.Context, target, hostEnvdPath string) (string, error) {
	if target == "" || target == "promoted" {
		return hostEnvdPath, nil
	}

	if !buildVersionRe.MatchString(target) {
		return "", fmt.Errorf("invalid build-envd-version target %q", target)
	}

	dir := filepath.Dir(hostEnvdPath)
	candidates := []string{
		filepath.Join(dir, "envd."+target),
		filepath.Join(dir, target, "envd"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	promotedVersion, err := GetEnvdVersion(ctx, hostEnvdPath)
	if err != nil {
		return "", fmt.Errorf("build-envd-version target %q is not staged under %q (tried %v), and the promoted binary's version is unreadable: %w", target, dir, candidates, err)
	}
	if strings.TrimPrefix(promotedVersion, "v") == strings.TrimPrefix(target, "v") {
		return hostEnvdPath, nil
	}

	return "", fmt.Errorf("build-envd-version target %q is not staged under %q (tried %v), and the promoted binary is v%s, not the target", target, dir, candidates, strings.TrimPrefix(promotedVersion, "v"))
}
