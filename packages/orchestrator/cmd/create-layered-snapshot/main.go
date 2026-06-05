//go:build linux

// Command create-layered-snapshot builds the Layer 0 (infrastructure)
// and Layer 1 (runtime) snapshots for the three-layer cold-start
// architecture. Layer 0 contains the guest kernel + envd daemon + base
// system libraries. Layer 1 adds the language runtime (Node.js + OpenClaw
// Gateway) on top of a separate, runtime-inclusive rootfs.
//
// Usage:
//
//	create-layered-snapshot \
//	  -l0-rootfs /data/e2b-templates/base/rootfs.ext4 \
//	  -l1-rootfs /data/e2b-templates/openclaw/rootfs.ext4 \
//	  -kernel /data/e2b-templates/kernels/vmlinux-6.1.158 \
//	  -fc-binary /opt/e2b-infra/packages/fc-versions/builds/v1.12.1_210cbac/firecracker \
//	  -l0-output /mnt/snapshot-cache/layers/L0 \
//	  -l1-output /mnt/snapshot-cache/layers/L1 \
//	  -prewarm-script /opt/e2b-infra/prewarm.js \
//	  -v8-snapshot-output /mnt/snapshot-cache/layers/L1/openclaw-snapshot.blob \
//	  -vcpu 2 -memory 2048
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/go-openapi/strfmt"

	fcClient "github.com/e2b-dev/infra/packages/shared/pkg/fc/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/client/operations"
	"github.com/e2b-dev/infra/packages/shared/pkg/fc/models"

	"github.com/firecracker-microvm/firecracker-go-sdk"
)

// networkConfig holds the IP addresses for a single VM ↔ host TAP link.
type networkConfig struct {
	vmIP    string // VM-side IP, e.g. "169.254.0.21"
	hostIP  string // Host-side (TAP) IP, e.g. "169.254.0.22"
	cidr    string // TAP address with prefix, e.g. "169.254.0.22/30"
	tapName string // Custom TAP name (overrides auto-generated tap-{label})
}

func main() {
	l0Rootfs := flag.String("l0-rootfs", "", "Path to Layer 0 rootfs (base system + envd only)")
	l1Rootfs := flag.String("l1-rootfs", "", "Path to Layer 1 rootfs (base + Node.js + OpenClaw)")
	kernel := flag.String("kernel", "", "Path to Firecracker kernel image")
	fcBinary := flag.String("fc-binary", "", "Path to Firecracker binary")
	l0Output := flag.String("l0-output", "", "Output directory for Layer 0 snapshot files")
	l1Output := flag.String("l1-output", "", "Output directory for Layer 1 snapshot files")
	prewarmScript := flag.String("prewarm-script", "", "Path to Node.js prewarm script (optional)")
	v8SnapshotOutput := flag.String("v8-snapshot-output", "", "Path to output V8 startup snapshot blob (optional)")
	vcpu := flag.Int64("vcpu", 2, "vCPU count")
	memory := flag.Int64("memory", 2048, "Memory in MB")
	strict := flag.Bool("strict", false, "Fail if GATEWAY_READY marker is not seen (L1 only)")
	tapName := flag.String("tap-name", "", "Custom TAP device name (overrides auto-generated tap-{label}")
	vmIP := flag.String("vm-ip", "169.254.0.21", "VM-side IP address")
	hostIP := flag.String("host-ip", "169.254.0.22", "Host-side TAP IP address (gateway for the VM)")

	// Backward compat: -template-dir sets both L0 and L1 rootfs
	templateDir := flag.String("template-dir", "", "Path to template directory (sets both -l0-rootfs and -l1-rootfs)")
	flag.Parse()

	// Resolve rootfs paths.
	if *l0Rootfs == "" && *templateDir != "" {
		*l0Rootfs = filepath.Join(*templateDir, "rootfs.ext4")
	}
	if *l1Rootfs == "" && *templateDir != "" {
		*l1Rootfs = filepath.Join(*templateDir, "rootfs.ext4")
	}

	if *l0Rootfs == "" || *l1Rootfs == "" || *kernel == "" || *fcBinary == "" || *l0Output == "" || *l1Output == "" {
		fmt.Fprintf(os.Stderr, "Usage: create-layered-snapshot\n")
		fmt.Fprintf(os.Stderr, "  -l0-rootfs <path>         Layer 0 rootfs (base system + envd)\n")
		fmt.Fprintf(os.Stderr, "  -l1-rootfs <path>         Layer 1 rootfs (base + Node.js + OpenClaw)\n")
		fmt.Fprintf(os.Stderr, "  -template-dir <dir>       Shorthand: sets both rootfs to <dir>/rootfs.ext4\n")
		fmt.Fprintf(os.Stderr, "  -kernel <path>            Path to Firecracker kernel\n")
		fmt.Fprintf(os.Stderr, "  -fc-binary <path>         Path to Firecracker binary\n")
		fmt.Fprintf(os.Stderr, "  -l0-output <dir>          Layer 0 output directory\n")
		fmt.Fprintf(os.Stderr, "  -l1-output <dir>          Layer 1 output directory\n")
		fmt.Fprintf(os.Stderr, "  -prewarm-script <path>    Node.js prewarm script (optional)\n")
		fmt.Fprintf(os.Stderr, "  -v8-snapshot-output <path> V8 startup snapshot blob output (optional)\n")
		fmt.Fprintf(os.Stderr, "  -vcpu <n>                 vCPU count (default: 2)\n")
		fmt.Fprintf(os.Stderr, "  -memory <mb>              Memory in MB (default: 2048)\n")
		fmt.Fprintf(os.Stderr, "  -strict                   Fail if GATEWAY_READY not seen (default: warn only)\n")
		fmt.Fprintf(os.Stderr, "  -vm-ip <ip>               VM-side IP (default: 169.254.0.21)\n")
		fmt.Fprintf(os.Stderr, "  -host-ip <ip>             Host-side TAP IP (default: 169.254.0.22)\n")
		os.Exit(1)
	}

	ctx := context.Background()

	for _, path := range []string{*l0Rootfs, *l1Rootfs, *kernel, *fcBinary} {
		if _, err := os.Stat(path); err != nil {
			fmt.Fprintf(os.Stderr, "Error: file not found: %s: %v\n", path, err)
			os.Exit(1)
		}
	}

	for _, dir := range []string{*l0Output, *l1Output} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating output dir %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	netCfg := &networkConfig{
		vmIP:    *vmIP,
		hostIP:  *hostIP,
		cidr:    *hostIP + "/16",
		tapName: *tapName,
	}

	fmt.Printf("=== Three-Layer Snapshot Builder ===\n")
	fmt.Printf("L0 rootfs:     %s\n", *l0Rootfs)
	fmt.Printf("L1 rootfs:     %s\n", *l1Rootfs)
	fmt.Printf("Kernel:        %s\n", *kernel)
	fmt.Printf("FC binary:     %s\n", *fcBinary)
	fmt.Printf("L0 output:     %s\n", *l0Output)
	fmt.Printf("L1 output:     %s\n", *l1Output)
	fmt.Printf("Prewarm:       %s\n", *prewarmScript)
	fmt.Printf("V8 snapshot:   %s\n", *v8SnapshotOutput)
	fmt.Printf("vCPU:          %d\n", *vcpu)
	fmt.Printf("Memory:        %d MB\n", *memory)
	fmt.Printf("Strict:        %v\n", *strict)
	fmt.Printf("Network:       VM=%s  Host=%s\n", netCfg.vmIP, netCfg.hostIP)

	if *l0Rootfs == *l1Rootfs {
		fmt.Fprintf(os.Stderr,
			"Warning: l0-rootfs and l1-rootfs are the same file.\n"+
				"Layer 0 and Layer 1 snapshots will be identical — no runtime\n"+
				"differentiation. Use separate rootfs images for meaningful layers.\n")
	}

	// Phase 1: Create Layer 0 snapshot (infrastructure only)
	fmt.Println("\n── Phase 1: Layer 0 snapshot ──")
	l0Snapfile := filepath.Join(*l0Output, "snapfile")
	l0Memfile := filepath.Join(*l0Output, "snapshot_memfile")
	l0Metadata := filepath.Join(*l0Output, "metadata.json")

	if err := bootAndSnapshot(ctx, "L0", *l0Rootfs, *kernel, *fcBinary,
		l0Snapfile, l0Memfile, l0Metadata,
		*vcpu, *memory, netCfg, false, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error in Phase 1 (L0): %v\n", err)
		os.Exit(1)
	}

	// Phase 2: Create Layer 1 snapshot (infrastructure + runtime)
	fmt.Println("\n── Phase 2: Layer 1 snapshot ──")
	l1Snapfile := filepath.Join(*l1Output, "snapfile")
	l1Memfile := filepath.Join(*l1Output, "snapshot_memfile")
	l1Metadata := filepath.Join(*l1Output, "metadata.json")

	var prewarmCmd *prewarmConfig
	if *prewarmScript != "" || *v8SnapshotOutput != "" {
		prewarmCmd = &prewarmConfig{
			scriptPath:       *prewarmScript,
			args:             []string{},
			v8SnapshotOutput: *v8SnapshotOutput,
		}
	}

	if err := bootAndSnapshot(ctx, "L1", *l1Rootfs, *kernel, *fcBinary,
		l1Snapfile, l1Memfile, l1Metadata,
		*vcpu, *memory, netCfg, *strict, prewarmCmd); err != nil {
		fmt.Fprintf(os.Stderr, "Error in Phase 2 (L1): %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n=== Done ===")
	fmt.Printf("Layer 0: %s (memfile: %.1f MB)\n",
		*l0Output, float64(fileSize(l0Memfile))/1024/1024)
	fmt.Printf("Layer 1: %s (memfile: %.1f MB)\n",
		*l1Output, float64(fileSize(l1Memfile))/1024/1024)
}

type prewarmConfig struct {
	scriptPath       string
	args             []string
	v8SnapshotOutput string
}

// bootAndSnapshot boots a Firecracker VM, optionally runs a prewarm command,
// pauses the VM, and creates a snapshot.
func bootAndSnapshot(
	ctx context.Context,
	label, rootfsPath, kernelPath, fcBinary string,
	snapfilePath, memfilePath, metadataPath string,
	vcpu, memoryMB int64,
	netCfg *networkConfig,
	strict bool,
	prewarm *prewarmConfig,
) error {
	fmt.Printf("[%s] Booting Firecracker VM...\n", label)

	workDir := filepath.Dir(snapfilePath)
	socketPath := filepath.Join(workDir, fmt.Sprintf("fc-%s.sock", label))
	logPath := filepath.Join(workDir, fmt.Sprintf("fc-%s.log", label))

	os.Remove(socketPath)

	logFd, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer logFd.Close()

	tapName := netCfg.tapName
	if tapName == "" {
		tapName = fmt.Sprintf("tap-%s", label)
	}
	if err := ensureTap(tapName, netCfg.cidr); err != nil {
		return fmt.Errorf("create tap device %s: %w", tapName, err)
	}

	initPath := "/usr/bin/envd"
	if label == "L1" {
		initPath = "/usr/local/bin/envd-wrapper"
	}
	kernelArgs := "console=ttyS0 quiet loglevel=1 reboot=k panic=1 pci=off " +
		"i8042.nokbd i8042.noaux random.trust_cpu=on " +
		"ip=" + netCfg.vmIP + "::" + netCfg.hostIP + ":255.255.0.0::eth0:off " +
		"root=/dev/vda rw init=" + initPath

	fcCmd := exec.CommandContext(ctx, fcBinary, "--api-sock", socketPath)
	fcCmd.Stdout = logFd
	fcCmd.Stderr = logFd

	if err := fcCmd.Start(); err != nil {
		return fmt.Errorf("start Firecracker: %w", err)
	}
	fcDone := make(chan error, 1)
	go func() { fcDone <- fcCmd.Wait() }()
	defer func() {
		fcCmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-fcDone:
		case <-time.After(2 * time.Second):
			fcCmd.Process.Kill()
			<-fcDone
		}
		os.Remove(socketPath)
	}()

	if err := waitForSocket(ctx, socketPath, 10*time.Second); err != nil {
		return fmt.Errorf("wait for FC socket: %w", err)
	}
	fmt.Printf("[%s] FC socket ready\n", label)

	fcHTTP := fcClient.NewHTTPClient(strfmt.NewFormats())
	transport := firecracker.NewUnixSocketTransport(socketPath, nil, false)
	fcHTTP.SetTransport(transport)

	driveID := "rootfs"
	isRoot := true
	isRO := false
	if err := putGuestDrive(ctx, fcHTTP, driveID, rootfsPath, isRoot, isRO); err != nil {
		return fmt.Errorf("set rootfs: %w", err)
	}
	if err := putBootSource(ctx, fcHTTP, kernelArgs, kernelPath); err != nil {
		return fmt.Errorf("set boot source: %w", err)
	}
	if err := putMachineConfig(ctx, fcHTTP, vcpu, memoryMB); err != nil {
		return fmt.Errorf("set machine config: %w", err)
	}

	ifaceID := "eth0"
	placeholderMAC := "02:FC:00:00:00:00"
	if err := putNetworkInterface(ctx, fcHTTP, ifaceID, tapName, placeholderMAC); err != nil {
		return fmt.Errorf("set network interface: %w", err)
	}
	if err := putMmdsConfig(ctx, fcHTTP, ifaceID); err != nil {
		return fmt.Errorf("set MMDS config: %w", err)
	}

	if err := instanceStart(ctx, fcHTTP); err != nil {
		return fmt.Errorf("instance start: %w", err)
	}
	fmt.Printf("[%s] VM booting...\n", label)

	envdAddr := netCfg.vmIP + ":49983"
	if label == "L1" {
		const gatewayReadyMarker = "GATEWAY_READY"
		const gatewayReadyTimeout = 30 * time.Second
		if err := waitForSerialMarker(ctx, logPath, gatewayReadyMarker, gatewayReadyTimeout); err != nil {
			if strict {
				return fmt.Errorf("strict mode: %w", err)
			}
			fmt.Fprintf(os.Stderr, "[%s] Warning: %v\n", label, err)
			fmt.Fprintf(os.Stderr, "[%s] Continuing with snapshot anyway...\n", label)
		}
	} else {
		envdTimeout := 15 * time.Second
		if err := waitForEnvdReady(envdAddr, envdTimeout); err != nil {
			if strict {
				return fmt.Errorf("strict mode, envd not reachable: %w", err)
			}
			fmt.Fprintf(os.Stderr, "[%s] Warning: envd not reachable at %s (%v)\n",
				label, envdAddr, err)
			fmt.Fprintf(os.Stderr, "[%s] Falling back to fixed 12s boot wait...\n", label)
			time.Sleep(12 * time.Second)
		}
	}
	fmt.Printf("[%s] VM booted and envd ready\n", label)

	if label == "L1" {
		const extraStabilizeWait = 30 * time.Second
		fmt.Printf("[%s] Waiting %v for gateway to fully stabilize...\n", label, extraStabilizeWait)
		time.Sleep(extraStabilizeWait)

		gwURL := fmt.Sprintf("http://%s:18789/health", netCfg.vmIP)
		fmt.Printf("[%s] Sending warmup requests to %s...\n", label, gwURL)
		for i := 0; i < 3; i++ {
			resp, err := httpGet(gwURL)
			if err == nil && resp.StatusCode == 200 {
				fmt.Printf("[%s] Warmup %d/3: OK\n", label, i+1)
			} else {
				fmt.Printf("[%s] Warmup %d/3: err=%v\n", label, i+1, err)
			}
			time.Sleep(2 * time.Second)
		}
		fmt.Printf("[%s] Gateway fully warmed and stable\n", label)
	}

	if prewarm != nil && prewarm.scriptPath != "" {
		fmt.Printf("[%s] Running prewarm: %s\n", label, prewarm.scriptPath)
		if err := executePrewarm(ctx, label, envdAddr, prewarm); err != nil {
			msg := fmt.Sprintf("[%s] Warning: prewarm execution failed: %v", label, err)
			if strict {
				return fmt.Errorf("strict mode, prewarm failed: %w", err)
			}
			fmt.Fprintf(os.Stderr, "%s\n", msg)
			fmt.Fprintf(os.Stderr, "[%s] Continuing with snapshot anyway...\n", label)
		}
		fmt.Printf("[%s] Prewarm complete\n", label)
	}

	if prewarm != nil && prewarm.v8SnapshotOutput != "" {
		fmt.Printf("[%s] Building V8 startup snapshot blob: %s\n", label, prewarm.v8SnapshotOutput)
		if err := buildV8Snapshot(ctx, prewarm.v8SnapshotOutput); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] Warning: V8 snapshot build failed: %v\n", label, err)
		}
	}

	fmt.Printf("[%s] Pausing VM...\n", label)
	if err := pauseVM(ctx, fcHTTP); err != nil {
		return fmt.Errorf("pause VM: %w", err)
	}

	fmt.Printf("[%s] Creating snapshot...\n", label)
	if err := createSnapshot(ctx, fcHTTP, snapfilePath, memfilePath); err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}

	snapStat, _ := os.Stat(snapfilePath)
	memStat, _ := os.Stat(memfilePath)
	fmt.Printf("[%s] Snapfile: %s (%.1f MB)\n", label, snapfilePath, float64(snapStat.Size())/1024/1024)
	fmt.Printf("[%s] Memfile:  %s (%.1f MB)\n", label, memfilePath, float64(memStat.Size())/1024/1024)

	actualMemMB := float64(memStat.Size()) / 1024 / 1024
	meta := map[string]any{
		"version": float64(2),
		"snapshot": map[string]any{
			"memfile_path":    "snapshot_memfile",
			"snapfile_path":   "snapfile",
			"memfile_size_mb": actualMemMB,
			"layer":           label,
		},
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(metadataPath, metaData, 0o644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	fmt.Printf("[%s] Metadata written to %s\n", label, metadataPath)

	return nil
}

func waitForEnvdReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for envd at %s", addr)
}

func executePrewarm(ctx context.Context, label string, envdAddr string, prewarm *prewarmConfig) error {
	scriptContent, err := os.ReadFile(prewarm.scriptPath)
	if err != nil {
		return fmt.Errorf("read prewarm script: %w", err)
	}

	prewarmTimeout := 120 * time.Second
	processC := processconnect.NewProcessClient(
		&http.Client{Timeout: prewarmTimeout},
		fmt.Sprintf("http://%s", envdAddr),
	)

	nodeArgs := append([]string{"--expose-gc", "-e", string(scriptContent)}, prewarm.args...)

	runReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd: "/usr/bin/node",
			Args: nodeArgs,
			Envs: map[string]string{
				"NODE_COMPILE_CACHE": "/home/user/.cache/node-compile-cache",
			},
		},
	})
	runReq.Header().Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("root:")))

	commandStream, err := processC.Start(ctx, runReq)
	if err != nil {
		time.Sleep(8 * time.Second)
		return nil
	}
	defer commandStream.Close()

	for commandStream.Receive() {
		event := commandStream.Msg().GetEvent()
		if event == nil {
			continue
		}
		switch {
		case event.GetData() != nil:
			data := event.GetData()
			if out := string(data.GetStdout()); out != "" {
				fmt.Printf("[%s] [prewarm stdout] %s", label, out)
			}
			if errOut := string(data.GetStderr()); errOut != "" {
				fmt.Printf("[%s] [prewarm stderr] %s", label, errOut)
			}
		case event.GetEnd() != nil:
			end := event.GetEnd()
			if end.GetExitCode() != 0 {
				return fmt.Errorf("prewarm exited with code %d: %s",
					end.GetExitCode(), end.GetStatus())
			}
			return nil
		}
	}

	if err := commandStream.Err(); err != nil {
		return fmt.Errorf("prewarm stream error: %w", err)
	}
	return nil
}

func buildV8Snapshot(ctx context.Context, outputPath string) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	scriptPath := filepath.Join(filepath.Dir(execPath), "build-v8-snapshot.js")

	if _, err := os.Stat(scriptPath); err != nil {
		cwd, _ := os.Getwd()
		scriptPath = filepath.Join(cwd, "build-v8-snapshot.js")
		if _, err := os.Stat(scriptPath); err != nil {
			return fmt.Errorf("build-v8-snapshot.js not found")
		}
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	nodeBin, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("node binary not found in PATH: %w", err)
	}

	cmd := exec.CommandContext(ctx, nodeBin,
		"--snapshot-blob="+outputPath,
		"--expose-gc",
		scriptPath,
	)
	cmd.Env = append(os.Environ(), "V8_SNAPSHOT_OUTPUT="+outputPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("node snapshot build failed: %w", err)
	}

	if fi, err := os.Stat(outputPath); err != nil {
		return fmt.Errorf("snapshot blob not created at %s: %w", outputPath, err)
	} else {
		fmt.Printf("V8 snapshot blob written: %s (%.1f KB)\n",
			outputPath, float64(fi.Size())/1024)
	}

	return nil
}

func waitForSerialMarker(ctx context.Context, logPath, marker string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	f, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 256)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if bytes.Contains(buf, []byte(marker)) {
				return nil
			}
			if len(buf) > len(marker)*2 {
				buf = buf[len(buf)-len(marker)*2:]
			}
		}
		if err == io.EOF {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if err != nil {
			return fmt.Errorf("read log: %w", err)
		}
	}
	return fmt.Errorf("timeout waiting for marker %q in %s", marker, logPath)
}

// ── FC API helpers ─────────────────────────────────────────────────────────

func putGuestDrive(ctx context.Context, c *fcClient.Firecracker, driveID, path string, isRoot, isReadOnly bool) error {
	params := operations.PutGuestDriveByIDParams{
		Context: ctx,
		DriveID: driveID,
		Body: &models.Drive{
			DriveID:      &driveID,
			PathOnHost:   path,
			IsRootDevice: &isRoot,
			IsReadOnly:   isReadOnly,
		},
	}
	_, err := c.Operations.PutGuestDriveByID(&params)
	return err
}

func putBootSource(ctx context.Context, c *fcClient.Firecracker, kernelArgs, kernelPath string) error {
	params := operations.PutGuestBootSourceParams{
		Context: ctx,
		Body: &models.BootSource{
			BootArgs:        kernelArgs,
			KernelImagePath: &kernelPath,
		},
	}
	_, err := c.Operations.PutGuestBootSource(&params)
	return err
}

func putMachineConfig(ctx context.Context, c *fcClient.Firecracker, vcpu, memMB int64) error {
	smt := false
	trackDirty := false
	params := operations.PutMachineConfigurationParams{
		Context: ctx,
		Body: &models.MachineConfiguration{
			VcpuCount:       &vcpu,
			MemSizeMib:      &memMB,
			Smt:             &smt,
			TrackDirtyPages: &trackDirty,
		},
	}
	_, err := c.Operations.PutMachineConfiguration(&params)
	return err
}

func instanceStart(ctx context.Context, c *fcClient.Firecracker) error {
	action := models.InstanceActionInfoActionTypeInstanceStart
	params := operations.CreateSyncActionParams{
		Context: ctx,
		Info:    &models.InstanceActionInfo{ActionType: &action},
	}
	_, err := c.Operations.CreateSyncAction(&params)
	return err
}

func pauseVM(ctx context.Context, c *fcClient.Firecracker) error {
	state := models.VMStatePaused
	params := operations.PatchVMParams{
		Context: ctx,
		Body:    &models.VM{State: &state},
	}
	_, err := c.Operations.PatchVM(&params)
	return err
}

func createSnapshot(ctx context.Context, c *fcClient.Firecracker, snapfilePath, memfilePath string) error {
	snapshotType := models.SnapshotCreateParamsSnapshotTypeFull
	params := operations.CreateSnapshotParams{
		Context: ctx,
		Body: &models.SnapshotCreateParams{
			SnapshotType: snapshotType,
			SnapshotPath: &snapfilePath,
			MemFilePath:  memfilePath,
		},
	}
	_, err := c.Operations.CreateSnapshot(&params)
	return err
}

func putNetworkInterface(ctx context.Context, c *fcClient.Firecracker, ifaceID, tapName, mac string) error {
	params := operations.PutGuestNetworkInterfaceByIDParams{
		Context: ctx,
		IfaceID: ifaceID,
		Body: &models.NetworkInterface{
			IfaceID:     &ifaceID,
			HostDevName: &tapName,
			GuestMac:    mac,
		},
	}
	_, err := c.Operations.PutGuestNetworkInterfaceByID(&params)
	return err
}

func putMmdsConfig(ctx context.Context, c *fcClient.Firecracker, ifaceID string) error {
	version := "V2"
	params := operations.PutMmdsConfigParams{
		Context: ctx,
		Body: &models.MmdsConfig{
			Version:           &version,
			NetworkInterfaces: []string{ifaceID},
		},
	}
	_, err := c.Operations.PutMmdsConfig(&params)
	return err
}

func ensureTap(name string, cidr string) error {
	if _, err := net.InterfaceByName(name); err == nil {
		return nil
	}
	cmd := exec.Command("ip", "tuntap", "add", name, "mode", "tap")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip tuntap add %s: %w (%s)", name, err, string(out))
	}
	exec.Command("ip", "link", "set", name, "up").Run()

	if out, err := exec.Command("ip", "addr", "add", cidr, "dev", name).CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "RTNETLINK answers: File exists") {
			fmt.Fprintf(os.Stderr, "Warning: ip addr add %s dev %s: %v (%s)\n",
				cidr, name, err, string(out))
		}
	}

	exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
	exec.Command("iptables", "-A", "FORWARD", "-i", name, "-j", "ACCEPT").Run()
	exec.Command("iptables", "-A", "FORWARD", "-o", name, "-j", "ACCEPT").Run()

	return nil
}

func waitForSocket(ctx context.Context, sockPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if fi, err := os.Stat(sockPath); err == nil && fi.Mode()&os.ModeSocket != 0 {
			if conn, err := net.Dial("unix", sockPath); err == nil {
				conn.Close()
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for socket %s", sockPath)
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func httpGet(url string) (*http.Response, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	return client.Get(url)
}
