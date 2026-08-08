//go:build linux

package finalize

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	tt "text/template"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const configurationTimeout = 5 * time.Minute

//go:embed configure.sh
var configureScriptFile string
var ConfigureScriptTemplate = tt.Must(tt.New("provisioning-finish-script").Parse(configureScriptFile))

// packCertBundleCmd regenerates the system trust store and packs it into a
// single contiguous tar. It is run as the build's last guest step — after all
// build steps, start_cmd, and ready_cmd — so the tar equals the trust store the
// guest would otherwise regenerate at boot (update-ca-certificates merges certs
// dropped under /usr/local/share/ca-certificates even if the user never ran it).
// envd.service seeds /etc/ssl/certs from this tar and skips update-ca-certificates
// on cold boot, trading the regen's scattered rootfs reads for one sequential
// read. -h dereferences the hash-named symlinks so the real cert contents are
// packed instead of links that would still fault the lazily-fetched rootfs.
// Deliberately Debian/Alpine-only. Adding an update-ca-trust branch for the RHEL
// family regresses it: extract regenerates the extracted/pem/directory-hash tree
// that /etc/ssl/certs points at, replacing the absolute ca-certificates.crt
// symlink provisioning created with a relative one that tar -h then packs as a
// link instead of dereferencing — the packed bundle drops from ~226 KB of PEM to
// a 20-byte symlink, and envd's egress-CA append needs a real file. Provisioning
// already refreshes the store per family; this step only has to merge CAs that
// later build layers dropped in, which is a Debian/Alpine convention anyway.
const packCertBundleCmd = `set -e
# Restore any package-owned directories under /etc/ssl/certs that exist in
# dpkg's file database but are absent in this VM's tmpfs. They were originally
# created by dpkg into a prior build-layer's tmpfs; finalize boots a fresh VM
# and e2b-seed-certs re-seeds /etc/ssl/certs from ssl-certs.tar, which never
# contained those subdirectories, so they silently vanish before this step.
# The canonical case: ca-certificates-java ships /etc/ssl/certs/java/ and its
# postinst writes cacerts there — if the directory is missing the trigger fails
# with FileNotFoundException, update-ca-certificates swallows the error, and
# the package is baked into the snapshot in half-configured (iF) state.
if command -v dpkg-query >/dev/null 2>&1; then
	dpkg-query -W -f='${db:Status-Abbrev} ${Package}\n' 2>/dev/null \\
		| awk '/^.i /{print $2}' \\
		| while IFS= read -r pkg; do dpkg -L "$pkg" 2>/dev/null; done \\
		| grep '^/etc/ssl/certs/' \\
		| sort -u \\
		| while IFS= read -r path; do
			[ -e "$path" ] || mkdir -p "$path"
		  done
fi
# Resolve any dpkg triggers still pending now that their required paths exist.
if command -v dpkg >/dev/null 2>&1; then
	dpkg --configure -a
fi
if command -v update-ca-certificates >/dev/null 2>&1; then
	update-ca-certificates
fi
# Fail the build explicitly if any package is still half-configured (iF).
# update-ca-certificates swallows hook failures, so without this check a
# broken dpkg state would be silently baked into the snapshot and every
# subsequent apt-get inside the sandbox would exit 100.
if command -v dpkg >/dev/null 2>&1; then
	broken=$(dpkg -l 2>/dev/null | awk '/^iF /{print $2}' | tr '\n' ' ')
	if [ -n "$broken" ]; then
		printf 'e2b cert bundle: broken dpkg packages after configure: %s\n' "$broken" >&2
		exit 1
	fi
fi
mkdir -p /usr/local/share/e2b
tar -C /etc/ssl/certs -chf /usr/local/share/e2b/ssl-certs.tar .
`

// packCertBundle runs packCertBundleCmd in the guest as root.
func packCertBundle(
	ctx context.Context,
	userLogger logger.Logger,
	proxy *proxy.SandboxProxy,
	sandboxID string,
) error {
	ctx, span := tracer.Start(ctx, "pack cert bundle")
	defer span.End()

	err := sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		userLogger,
		zapcore.DebugLevel,
		"certs",
		sandboxID,
		packCertBundleCmd,
		metadata.Context{
			User: "root",
		},
	)
	if err != nil {
		return fmt.Errorf("error packing CA cert bundle: %w", err)
	}

	return nil
}

type ConfigurationParams struct {
	EnvID      string
	TemplateID string
	BuildID    string
}

func runConfiguration(
	ctx context.Context,
	userLogger logger.Logger,
	bc buildcontext.BuildContext,
	proxy *proxy.SandboxProxy,
	sandboxID string,
) error {
	ctx, span := tracer.Start(ctx, "run configuration")
	defer span.End()

	// Run configuration script
	var scriptDef bytes.Buffer
	err := ConfigureScriptTemplate.Execute(&scriptDef, ConfigurationParams{
		EnvID:      bc.Config.TemplateID,
		TemplateID: bc.Config.TemplateID,
		BuildID:    bc.Template.BuildID,
	})
	if err != nil {
		return fmt.Errorf("error executing provision script: %w", err)
	}

	err = sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		userLogger,
		zapcore.DebugLevel,
		"config",
		sandboxID,
		scriptDef.String(),
		metadata.Context{
			User: "root",
		},
	)
	if err != nil {
		return fmt.Errorf("error running configuration script: %w", err)
	}

	return nil
}
