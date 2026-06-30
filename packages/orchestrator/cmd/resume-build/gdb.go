package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// gdbOptions controls the `resume-build -gdb` guest-kernel debugging flow.
type gdbOptions struct {
	enabled bool

	// Artifact overrides. By default resume-build fetches the debug artifacts by
	// version (firecracker-debug and vmlinux.debug) from the release buckets, the
	// same way create-build fetches the prod kernel/FC. These override the fetch with
	// a local path: a Firecracker built `--features gdb` and the kernel's split DWARF
	// symbols (vmlinux.debug).
	fcBinary string // -gdb-fc
	symbols  string // -gdb-symbols

	socket   string // -gdb-socket (optional; default: a temp path)
	execCmds string // -gdb-exec   (scripted mode: gdb commands, newline/';'-separated)
	script   string // -gdb-script (scripted mode: a gdb command file)
}

func (o gdbOptions) scripted() bool { return o.execCmds != "" || o.script != "" }

// gdbMode resumes the snapshot with Firecracker's gdb stub armed (held at the entry
// breakpoint), loads the guest kernel symbols, hands a ready gdb session to the
// caller, and tears everything down on exit.
func (r *runner) gdbMode(ctx context.Context, opts gdbOptions) error {
	// 1. Fail fast, before we touch the sandbox or fetch anything: gdb must be on
	//    PATH (checked first so we don't download a ~500 MB vmlinux.debug only to
	//    fall over here), then resolve the debug artifacts — the -gdb-* overrides if
	//    given, else a local staged copy, else fetch them by version.
	if _, err := exec.LookPath("gdb"); err != nil {
		return fmt.Errorf("gdb not found on PATH: %w", err)
	}
	fcBinary, symbols, err := r.gdbResolveArtifacts(ctx, opts)
	if err != nil {
		return err
	}

	// 2. The kernel image runs at its link-time base — there is no KASLR slide to
	//    recover. Firecracker boots the uncompressed vmlinux ELF directly (no bzImage
	//    decompressor), so the image-KASLR relocation never runs and both
	//    CONFIG_RANDOMIZE_BASE and CONFIG_RANDOMIZE_MEMORY stay inert (gated on a flag
	//    only the decompressor sets). So we load symbols at offset 0.

	// 3. Stage the gdb-enabled Firecracker binary at the path resume-build resolves
	//    for this snapshot's FC version, backing up whatever is there and restoring
	//    it on exit (so the local prod binary is left untouched).
	fcPath := r.sbxConfig.FirecrackerConfig.FirecrackerPath(r.config)
	restore, err := stageBinary(fcBinary, fcPath)
	if err != nil {
		return fmt.Errorf("stage debug firecracker: %w", err)
	}
	defer restore()

	// 4. Arm the stub via the env var (FC inherits resume-build's env; no jailer
	//    here), and tell the resume path not to wait for envd — the guest never
	//    boots it while held at the entry breakpoint.
	socket := opts.socket
	if socket == "" {
		socket = filepath.Join(os.TempDir(), fmt.Sprintf("fc-gdb-%d.sock", time.Now().UnixNano()))
	}
	_ = os.Remove(socket)
	if err := os.Setenv("FIRECRACKER_GDB_SOCKET", socket); err != nil {
		return fmt.Errorf("set FIRECRACKER_GDB_SOCKET: %w", err)
	}
	r.sbxConfig.SkipEnvdWait = true

	// 5. Resume concurrently. Firecracker's gdb stub holds the snapshot load open
	//    until a debugger attaches, so ResumeSandbox does not return until we connect
	//    gdb. Run it in the background and connect gdb once FC binds the socket; doing
	//    it the other way around (resume, then connect) deadlocks.
	runtime := sandbox.RuntimeMetadata{
		TemplateID:  r.buildID,
		TeamID:      "local",
		SandboxID:   fmt.Sprintf("sbx-gdb-%d", time.Now().UnixNano()),
		ExecutionID: fmt.Sprintf("exec-gdb-%d", time.Now().UnixNano()),
	}
	fmt.Println("🚀 Resuming under gdb (guest held at entry breakpoint)...")
	t0 := time.Now()

	type startResult struct {
		sbx *sandbox.Sandbox
		err error
	}
	resumeCtx, cancelResume := context.WithCancel(ctx)
	startCh := make(chan startResult, 1)
	go func() {
		sbx, err := r.startSandbox(resumeCtx, runtime, t0, t0.Add(24*time.Hour))
		startCh <- startResult{sbx: sbx, err: err}
	}()
	defer func() {
		// Unblock ResumeSandbox if it is still waiting (e.g. gdb never connected),
		// then reclaim the sandbox once it returns.
		cancelResume()
		res := <-startCh
		if res.sbx != nil {
			fmt.Println("🧹 Cleanup...")
			res.sbx.Close(context.WithoutCancel(ctx))
		}
		_ = os.Remove(socket)
	}()

	// FC binds the gdb socket while loading the snapshot — before the load blocks on
	// the debugger — so the socket appears even though startSandbox is still running.
	// Race the socket wait against the resume result: if the resume fails before the
	// socket appears, surface that real error instead of a misleading socket timeout.
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	socketErr := make(chan error, 1)
	go func() { socketErr <- waitForSocket(waitCtx, socket, 90*time.Second) }()

	select {
	case res := <-startCh:
		startCh <- res // hand back to the deferred cleanup
		if res.err != nil {
			return fmt.Errorf("resume: %w", res.err)
		}

		return fmt.Errorf("resume completed without binding gdb socket %s", socket)
	case err := <-socketErr:
		if err != nil {
			return fmt.Errorf("gdb socket %s never appeared: %w", socket, err)
		}
	}
	fmt.Printf("✅ FC bound gdb socket in %s\n", time.Since(t0))

	// 6. Generate the parameterized init script and print the debug-context block.
	initScript, err := writeInitScript(symbols, socket)
	if err != nil {
		return fmt.Errorf("write gdb init script: %w", err)
	}
	defer os.Remove(initScript)

	printGdbContext(fcPath, r.sbxConfig.FirecrackerConfig.FirecrackerVersion, symbols, socket, initScript)

	// 7. Drive gdb. FC's stub shuts the VM down on disconnect, so this is one session
	//    per invocation; teardown (defer above) reclaims FC/UFFD/NBD/temp.
	return runGdb(ctx, initScript, opts)
}

// defaultDebugArtifactsBaseURL is where the fc-versions / fc-kernels release pipelines
// publish the debug artifacts (firecracker-debug, vmlinux.debug), alongside the prod
// firecracker/vmlinux that create-build already fetches from here.
const defaultDebugArtifactsBaseURL = "https://storage.googleapis.com/e2b-prod-public-builds"

// debugArtifactsBaseURL is the base URL to fetch firecracker-debug / vmlinux.debug from.
// Overridable via E2B_GDB_ARTIFACTS_URL (e.g. to point at a bucket you can read before
// the artifacts are published to the public one).
func debugArtifactsBaseURL() string {
	if u := os.Getenv("E2B_GDB_ARTIFACTS_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}

	return defaultDebugArtifactsBaseURL
}

// gdbResolveArtifacts resolves the debug FC binary and the vmlinux.debug symbols. Each
// is taken from its -gdb-* override if set, else a local staged copy if present, else
// fetched by version from the release buckets (see debugArtifactsBaseURL) — mirroring
// how create-build fetches the prod kernel/FC.
func (r *runner) gdbResolveArtifacts(ctx context.Context, opts gdbOptions) (fcBinary, symbols string, err error) {
	arch := utils.TargetArch()
	fcVer := r.sbxConfig.FirecrackerConfig.FirecrackerVersion
	kernelVer := r.sbxConfig.FirecrackerConfig.KernelVersion
	fcDir := filepath.Dir(r.sbxConfig.FirecrackerConfig.FirecrackerPath(r.config))
	kernelDir := filepath.Dir(r.sbxConfig.FirecrackerConfig.HostKernelPath(r.config))
	base := debugArtifactsBaseURL()

	fcURL, err := url.JoinPath(base, "firecrackers", fcVer, arch, "firecracker-debug")
	if err != nil {
		return "", "", fmt.Errorf("firecracker-debug URL: %w", err)
	}
	symURL, err := url.JoinPath(base, "kernels", kernelVer, arch, "vmlinux.debug")
	if err != nil {
		return "", "", fmt.Errorf("vmlinux.debug URL: %w", err)
	}

	fcBinary, fcErr := resolveOrFetch(ctx, opts.fcBinary, filepath.Join(fcDir, "firecracker-debug"), fcURL, 0o755)
	symbols, symErr := resolveOrFetch(ctx, opts.symbols, filepath.Join(kernelDir, "vmlinux.debug"), symURL, 0o644)

	var missing []string
	if fcErr != nil {
		missing = append(missing, fmt.Sprintf("firecracker-debug (FC %s): %v", fcVer, fcErr))
	}
	if symErr != nil {
		missing = append(missing, fmt.Sprintf("vmlinux.debug (kernel %s): %v", kernelVer, symErr))
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf(
			"could not obtain gdb debug artifacts:\n  - %s\n"+
				"They are fetched by version from %s. Until the fc-versions/fc-kernels release\n"+
				"pipelines publish them there, build them (a --features gdb firecracker and a DWARF\n"+
				"kernel) and pass -gdb-fc / -gdb-symbols, or set E2B_GDB_ARTIFACTS_URL to a base URL\n"+
				"that serves them",
			strings.Join(missing, "\n  - "), base)
	}

	return fcBinary, symbols, nil
}

// resolveOrFetch returns the override if it is set (erroring if it does not exist),
// otherwise the local staged path if it already exists, otherwise downloads url to it.
func resolveOrFetch(ctx context.Context, override, localPath, srcURL string, perm os.FileMode) (string, error) {
	if override != "" {
		if fileExists(override) {
			return override, nil
		}

		return "", fmt.Errorf("override path %s does not exist", override)
	}
	if fileExists(localPath) {
		return localPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return "", err
	}
	fmt.Printf("⬇ fetching %s from %s ...\n", filepath.Base(localPath), srcURL)
	if err := download(ctx, srcURL, localPath, perm); err != nil {
		return "", err
	}

	return localPath, nil
}

// download GETs rawURL to path (atomic rename via a .tmp). Mirrors create-build's
// helper; the debug artifacts live in the same public release buckets.
func download(ctx context.Context, rawURL, path string, perm os.FileMode) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("invalid download URL %s: %w", rawURL, err)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not found (HTTP 404): %s", rawURL)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, rawURL)
	}

	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpPath)

		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)

		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)

		return err
	}

	return nil
}

// writeInitScript generates the parameterized gdb init script: load the versioned
// macro library, the symbols (at their link-time addresses — see gdbMode: FC boots the
// vmlinux ELF directly, so there is no KASLR image slide), and connect to the stub.
func writeInitScript(symbols, socket string) (string, error) {
	macroLib, err := macroLibPath()
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "fc-debug-init-*.gdb")
	if err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(f, `set pagination off
set confirm off
source %s
# FC boots the uncompressed vmlinux ELF directly, so KASLR never relocates the image:
# symbols sit at their link-time addresses (offset 0).
add-symbol-file %s -o 0x0
target remote %s
`, macroLib, symbols, socket); err != nil {
		f.Close()
		_ = os.Remove(f.Name())

		return "", fmt.Errorf("write gdb init script: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())

		return "", fmt.Errorf("close gdb init script: %w", err)
	}

	return f.Name(), nil
}

// macroLibPath locates the checked-in fc-debug.gdb macro library next to this binary's
// source. resume-build is run via `go run`/built from cmd/resume-build, so the library
// sits beside main.go.
func macroLibPath() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		if p := filepath.Join(filepath.Dir(exe), "fc-debug.gdb"); fileExists(p) {
			return p, nil
		}
	}
	// Fall back to the source tree (the common `go run ./cmd/resume-build` case).
	for _, p := range []string{
		"cmd/resume-build/fc-debug.gdb",
		"packages/orchestrator/cmd/resume-build/fc-debug.gdb",
	} {
		if abs, err := filepath.Abs(p); err == nil && fileExists(abs) {
			return abs, nil
		}
	}

	return "", errors.New("fc-debug.gdb macro library not found (next to the binary or under cmd/resume-build/)")
}

// printGdbContext prints the debug-context block so the session is drivable any way
// (interactive, batch, or a long-lived agent-driven gdb subprocess).
func printGdbContext(fcPath, fcVer, symbols string, socket, initScript string) {
	fmt.Println("\n──────────────── gdb debug context ────────────────")
	fmt.Printf("  debug firecracker : %s (version %s)\n", fcPath, fcVer)
	fmt.Printf("  kernel symbols    : %s (link addresses; FC ELF boot, no KASLR slide)\n", symbols)
	fmt.Printf("  gdb socket        : %s\n", socket)
	fmt.Printf("  gdb init script   : %s\n", initScript)
	fmt.Printf("  attach manually   : gdb -q -x %s\n", initScript)
	fmt.Println("  macros            : fc-faults [N], fc-curr, fc-task <p>, fc-regions, fc-va <phys>")
	fmt.Println("────────────────────────────────────────────────────")
}

// runGdb runs gdb against the generated init script: interactive by default, or
// batch when -gdb-exec / -gdb-script is given (the agent/CI path).
func runGdb(ctx context.Context, initScript string, opts gdbOptions) error {
	var args []string
	if opts.scripted() {
		args = append(args, "-batch")
	} else {
		args = append(args, "-q")
	}
	args = append(args, "-x", initScript)
	if opts.script != "" {
		args = append(args, "-x", opts.script)
	}
	for _, line := range strings.FieldsFunc(opts.execCmds, func(r rune) bool { return r == '\n' || r == ';' }) {
		if cmd := strings.TrimSpace(line); cmd != "" {
			args = append(args, "-ex", cmd)
		}
	}

	cmd := exec.CommandContext(ctx, "gdb", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if !opts.scripted() {
		fmt.Printf("🐞 launching gdb (VM shuts down on disconnect)...\n\n")
	}

	return cmd.Run()
}

// --- small helpers ---

func fileExists(p string) bool {
	info, err := os.Stat(p)

	return err == nil && !info.IsDir()
}

func waitForSocket(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timeout")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// stageBinary copies src to dst (preserving any existing dst as a backup) and returns
// a restore func that puts the original back (or removes the staged copy if there was
// none). The running FC keeps its loaded binary, so restoring after launch is safe.
func stageBinary(src, dst string) (restore func(), err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	bak := dst + ".prodbak"
	// If a backup already exists, a previous run was interrupted before it could
	// restore — that backup is the real binary, so keep it and overwrite dst (which
	// currently holds the staged debug binary) rather than backing dst up over it.
	hadOriginal := fileExists(bak)
	if !hadOriginal && fileExists(dst) {
		if err := os.Rename(dst, bak); err != nil {
			return nil, fmt.Errorf("back up %s: %w", dst, err)
		}
		hadOriginal = true
	}
	if err := copyFile(src, dst, 0o755); err != nil {
		if hadOriginal {
			_ = os.Rename(bak, dst)
		} else {
			// No original to restore: drop the partial/truncated copy so a later
			// run can't resolve and execute a corrupt binary at dst.
			_ = os.Remove(dst)
		}

		return nil, err
	}

	return func() {
		_ = os.Remove(dst)
		if hadOriginal {
			_ = os.Rename(bak, dst)
		}
	}, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()

		return err
	}

	return out.Close()
}
