// migrate-demo demonstrates cross-node sandbox migration with live connectivity test.
//
// Usage:
//
//	sudo ./migrate-demo --interactive \
//	  --source=localhost:5008 --target=192.168.100.137:5008 \
//	  --template=<build-uuid> \
//	  --source-storage=/path/to/local-template-storage \
//	  --target-storage=/path/to/local-template-storage
package main

import (
	"context"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	orchestrator "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

var (
	sourceAddr      = flag.String("source", "localhost:5008", "Source orchestrator gRPC address")
	targetAddr      = flag.String("target", "192.168.100.137:5008", "Target orchestrator gRPC address")
	sandboxID       = flag.String("sandbox-id", "", "Existing sandbox ID to migrate (if empty, creates one)")
	templateID      = flag.String("template", "base", "Build ID (UUID) from template cache")
	kernelVer       = flag.String("kernel", "vmlinux-6.1.158", "Kernel version for sandbox creation")
	fcVer           = flag.String("fc-version", "v1.12.1_a41d3fb", "Firecracker version for sandbox creation")
	sourceWgIP      = flag.String("source-wg-ip", "10.99.0.2", "Source node WireGuard IP")
	targetWgIP      = flag.String("target-wg-ip", "10.99.0.1", "Target node WireGuard IP")
	wgDevice        = flag.String("wg-device", "wg0", "WireGuard interface name")
	sourceStorageDir = flag.String("source-storage", "", "Source local template storage dir")
	targetStorageDir = flag.String("target-storage", "", "Target local template storage dir")
	sourceCacheDir  = flag.String("source-cache", "/orchestrator/template", "Source template cache dir")
	targetCacheDir  = flag.String("target-cache", "/orchestrator/template", "Target template cache dir")
	sourceDiffCache = flag.String("source-diff-cache", "/orchestrator/build", "Source diff cache dir")
	targetDiffCache = flag.String("target-diff-cache", "/orchestrator/build", "Target diff cache dir")
	interactive     = flag.Bool("interactive", false, "Pause at each stage for manual exploration")
	cleanupAfter    = flag.Bool("cleanup", true, "Delete sandbox after demo")
	timeout         = flag.Duration("timeout", 5*time.Minute, "Overall timeout")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	start := time.Now()
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	// Connect
	logStep(start, "Connecting to source (%s) and target (%s)...", *sourceAddr, *targetAddr)
	sourceConn, err := grpc.DialContext(ctx, *sourceAddr, opts...)
	if err != nil {
		fatalf("connect source: %v", err)
	}
	defer sourceConn.Close()

	targetConn, err := grpc.DialContext(ctx, *targetAddr, opts...)
	if err != nil {
		fatalf("connect target: %v", err)
	}
	defer targetConn.Close()

	sourceClient := orchestrator.NewSandboxServiceClient(sourceConn)
	targetClient := orchestrator.NewSandboxServiceClient(targetConn)

	srcCount, tgtCount := countSandboxes(ctx, sourceClient, targetClient)
	logStep(start, "Connected. Sandboxes: w1=%d  box=%d", srcCount, tgtCount)

	// Step 1: Create sandbox
	sbxID := *sandboxID
	migBuildID := uuid.New().String()

	if sbxID == "" {
		logStep(start, "Creating sandbox on w1 (build=%s)...", *templateID)
		sbxID = fmt.Sprintf("mig-demo-%d", time.Now().UnixMilli())
		now := time.Now()
		_, err = sourceClient.Create(ctx, &orchestrator.SandboxCreateRequest{
			Sandbox: &orchestrator.SandboxConfig{
				TemplateId:         *templateID,
				BuildId:            *templateID,
				SandboxId:          sbxID,
				KernelVersion:      *kernelVer,
				FirecrackerVersion: *fcVer,
			},
			StartTime: timestamppb.New(now),
			EndTime:   timestamppb.New(now.Add(1 * time.Hour)),
		})
		if err != nil {
			fatalf("create sandbox: %v", err)
		}
		logStep(start, "Sandbox created: %s", sbxID)
		time.Sleep(3 * time.Second)
	} else {
		logStep(start, "Using existing sandbox: %s", sbxID)
	}

	// Find the sandbox's host IP by looking for the FC process
	hostIP := findSandboxHostIP(sbxID)
	srcCount, tgtCount = countSandboxes(ctx, sourceClient, targetClient)
	logStep(start, "")
	logStep(start, "=== SANDBOX RUNNING ON W1 ===")
	logStep(start, "  ID:    %s", sbxID)
	logStep(start, "  IP:    %s", hostIP)
	logStep(start, "  Count: w1=%d  box=%d", srcCount, tgtCount)

	// Run a command to prove it's alive
	logStep(start, "  Exec:  hostname && uptime")
	output := envdExec(hostIP, "/bin/bash", "-l", "-c", "hostname && uptime")
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		logStep(start, "         > %s", line)
	}

	// Start a background connectivity probe: curl every 0.5s, log timestamp + status
	logStep(start, "  Starting connectivity probe (curl example.com every 0.5s)...")
	envdExec(hostIP, "/bin/bash", "-l", "-c",
		`rm -f /tmp/probe.log; while true; do
			TS=$(date +%H:%M:%S.%N | cut -c1-12)
			CODE=$(curl -sf --max-time 2 -o /dev/null -w '%{http_code}' http://example.com 2>/dev/null || echo 000)
			echo "$TS $CODE" >> /tmp/probe.log
			sleep 0.5
		done &`)
	// Let a few probes run
	time.Sleep(3 * time.Second)
	probeCheck := envdExec(hostIP, "/bin/bash", "-l", "-c", "tail -3 /tmp/probe.log")
	for _, line := range strings.Split(strings.TrimSpace(probeCheck), "\n") {
		logStep(start, "  Probe:  %s", line)
	}

	if *interactive {
		logStep(start, "")
		logStep(start, "Press ENTER to start migration (probe keeps running)...")
		fmt.Scanln()
	}

	// Step 2: Capture config
	start = time.Now() // reset timer for migration
	logStep(start, "Capturing sandbox config...")
	sbxConfig := captureSandboxConfig(ctx, sourceClient, sbxID)
	logStep(start, "Config: vcpu=%d ram=%dMB kernel=%s fc=%s",
		sbxConfig.GetVcpu(), sbxConfig.GetRamMb(),
		sbxConfig.GetKernelVersion(), sbxConfig.GetFirecrackerVersion())

	// Step 3: Pause
	logStep(start, "Pausing sandbox on w1...")
	pauseStart := time.Now()
	_, err = sourceClient.Pause(ctx, &orchestrator.SandboxPauseRequest{
		SandboxId:  sbxID,
		TemplateId: *templateID,
		BuildId:    migBuildID,
	})
	if err != nil {
		fatalf("pause: %v", err)
	}
	pauseDur := time.Since(pauseStart)
	logStep(start, "Paused (took %s)", pauseDur.Round(time.Millisecond))

	// Step 4: Transfer
	logStep(start, "Transferring snapshot via WireGuard...")
	transferStart := time.Now()
	if err := transferSnapshot(ctx, migBuildID); err != nil {
		fatalf("transfer: %v", err)
	}
	transferDur := time.Since(transferStart)
	logStep(start, "Transferred (took %s)", transferDur.Round(time.Millisecond))

	// Step 5: Resume on target
	logStep(start, "Resuming on box...")
	resumeStart := time.Now()
	resumeCfg := proto.Clone(sbxConfig).(*orchestrator.SandboxConfig)
	resumeCfg.Snapshot = true
	resumeCfg.BuildId = migBuildID
	resumeCfg.BaseTemplateId = sbxConfig.GetTemplateId()
	resumeCfg.ExecutionId = uuid.New().String()
	newSbxID := sbxID
	now := time.Now()
	_, err = targetClient.Create(ctx, &orchestrator.SandboxCreateRequest{
		Sandbox:   resumeCfg,
		StartTime: timestamppb.New(now),
		EndTime:   timestamppb.New(now.Add(1 * time.Hour)),
	})
	if err != nil {
		fatalf("resume on target: %v", err)
	}
	resumeDur := time.Since(resumeStart)
	logStep(start, "Resumed (took %s)", resumeDur.Round(time.Millisecond))

	// Verify on target
	verifyOnNode(ctx, targetClient, newSbxID, "box")

	// Find host IP on box
	tgtHostIP := findSandboxHostIPRemote(sbxID)
	srcCount, tgtCount = countSandboxes(ctx, sourceClient, targetClient)

	logStep(start, "")
	logStep(start, "=== SANDBOX NOW ON BOX ===")
	logStep(start, "  ID:    %s", newSbxID)
	logStep(start, "  IP:    %s (on box)", tgtHostIP)
	logStep(start, "  Count: w1=%d  box=%d", srcCount, tgtCount)

	// Run a command on the migrated sandbox
	logStep(start, "  Exec:  hostname && uptime")
	output = envdExecRemote(tgtHostIP, "/bin/bash", "-l", "-c", "hostname && uptime")
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		logStep(start, "         > %s", line)
	}

	// Wait for a few probes to run after migration, then read the full log.
	// The probe loop survives snapshot/restore — it was running in the VM.
	time.Sleep(4 * time.Second)
	logStep(start, "  Reading connectivity probe log...")
	probeLog := envdExecRemote(tgtHostIP, "/bin/bash", "-l", "-c", "cat /tmp/probe.log 2>/dev/null")
	probeLines := strings.Split(strings.TrimSpace(probeLog), "\n")

	// Count and display
	var ok, fail, total int
	for _, line := range probeLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		total++
		if strings.HasSuffix(line, " 200") {
			ok++
		} else {
			fail++
		}
	}

	logStep(start, "")
	logStep(start, "  === Connectivity Probe Results ===")
	// Show first few (before migration)
	showCount := 3
	if showCount > len(probeLines) {
		showCount = len(probeLines)
	}
	for _, line := range probeLines[:showCount] {
		logStep(start, "    %s", strings.TrimSpace(line))
	}
	if total > 6 {
		logStep(start, "    ...")
		// Show the transition area (middle of the log where the gap is)
		mid := len(probeLines) / 2
		gapStart := mid - 2
		if gapStart < showCount {
			gapStart = showCount
		}
		gapEnd := mid + 3
		if gapEnd > len(probeLines) {
			gapEnd = len(probeLines)
		}
		for _, line := range probeLines[gapStart:gapEnd] {
			logStep(start, "    %s", strings.TrimSpace(line))
		}
		logStep(start, "    ...")
	}
	// Show last few (after migration)
	tailStart := len(probeLines) - 3
	if tailStart < 0 {
		tailStart = 0
	}
	for _, line := range probeLines[tailStart:] {
		logStep(start, "    %s", strings.TrimSpace(line))
	}
	logStep(start, "  Total: %d probes, %d OK (200), %d failed", total, ok, fail)

	if *interactive {
		logStep(start, "")
		logStep(start, "Press ENTER to print summary and clean up...")
		fmt.Scanln()
	}

	// Summary
	downtimeWindow := pauseDur + resumeDur
	totalDur := time.Since(start)
	fmt.Println()
	fmt.Println("=== Migration Summary ===")
	fmt.Printf("  Sandbox:   %s\n", sbxID)
	fmt.Printf("  Source:    w1 (%s) -> box (%s)\n", *sourceAddr, *targetAddr)
	fmt.Printf("  Pause:     %s\n", pauseDur.Round(time.Millisecond))
	fmt.Printf("  Transfer:  %s\n", transferDur.Round(time.Millisecond))
	fmt.Printf("  Resume:    %s\n", resumeDur.Round(time.Millisecond))
	fmt.Printf("  Downtime:  ~%s\n", downtimeWindow.Round(time.Millisecond))
	fmt.Printf("  Total:     %s\n", totalDur.Round(time.Millisecond))
	fmt.Println()

	// Cleanup
	if *cleanupAfter {
		logStep(start, "Cleaning up...")
		_, err = targetClient.Delete(ctx, &orchestrator.SandboxDeleteRequest{SandboxId: newSbxID})
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: cleanup failed: %v\n", err)
		} else {
			logStep(start, "Sandbox deleted on box")
		}
	}
	logStep(start, "Done!")
}

// --- envd helpers ---

// envdExec runs a command in a sandbox via envd Connect RPC (local node).
func envdExec(hostIP string, cmd string, args ...string) string {
	return envdCall(hostIP, cmd, args)
}

// envdExecRemote runs a command in a sandbox on box via SSH + envd.
// We write a temp Python script, scp it to box, and execute it there
// to avoid all shell quoting issues with nested SSH.
func envdExecRemote(hostIP string, cmd string, args ...string) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"process": map[string]interface{}{"cmd": cmd, "args": args},
	})
	payloadB64 := base64.StdEncoding.EncodeToString(payload)

	// Write a self-contained Python script to /tmp
	script := fmt.Sprintf(`import base64,struct,urllib.request,sys
p=base64.b64decode("%s")
e=struct.pack(">BI",0,len(p))+p
r=urllib.request.Request("http://%s:49983/process.Process/Start",data=e,headers={"Content-Type":"application/connect+json","Connect-Protocol-Version":"1"})
sys.stdout.buffer.write(urllib.request.urlopen(r,timeout=10).read())
`, payloadB64, hostIP)

	tmpFile := fmt.Sprintf("/tmp/envd-call-%d.py", time.Now().UnixNano())
	os.WriteFile(tmpFile, []byte(script), 0644)
	defer os.Remove(tmpFile)

	// SCP to box and execute
	exec.Command("scp", "-o", "StrictHostKeyChecking=no", tmpFile, "root@10.99.0.1:"+tmpFile).Run()
	out, err := exec.Command("ssh", "-o", "StrictHostKeyChecking=no",
		"root@10.99.0.1", "python3", tmpFile).Output()
	// Cleanup remote
	exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "root@10.99.0.1", "rm", "-f", tmpFile).Run()

	if err != nil {
		return fmt.Sprintf("(envd remote error: %v)", err)
	}
	return decodeEnvdOutput(string(out))
}

// envdCall makes a Connect RPC call to envd.
func envdCall(hostIP string, cmd string, args []string) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"process": map[string]interface{}{
			"cmd":  cmd,
			"args": args,
		},
	})

	// Connect protocol envelope: flags(1) + length(4) + payload
	envelope := make([]byte, 5+len(payload))
	envelope[0] = 0 // flags
	binary.BigEndian.PutUint32(envelope[1:5], uint32(len(payload)))
	copy(envelope[5:], payload)

	req, _ := http.NewRequest("POST",
		fmt.Sprintf("http://%s:49983/process.Process/Start", hostIP),
		bytes.NewReader(envelope))
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("(envd error: %v)", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	return decodeEnvdOutput(string(body))
}

// decodeEnvdOutput extracts and decodes base64 stdout from envd Connect streaming response.
// The response is a sequence of envelope-framed JSON messages:
//
//	[flags:1][length:4][json-payload]  [flags:1][length:4][json-payload]  ...
func decodeEnvdOutput(raw string) string {
	data := []byte(raw)
	var result strings.Builder

	for len(data) >= 5 {
		// Read envelope: 1 byte flags + 4 byte big-endian length
		msgLen := binary.BigEndian.Uint32(data[1:5])
		data = data[5:]

		if int(msgLen) > len(data) {
			break
		}

		chunk := data[:msgLen]
		data = data[msgLen:]

		var obj map[string]interface{}
		if err := json.Unmarshal(chunk, &obj); err != nil {
			continue
		}

		event, ok := obj["event"].(map[string]interface{})
		if !ok {
			continue
		}
		eventData, ok := event["data"].(map[string]interface{})
		if !ok {
			continue
		}
		if stdout, ok := eventData["stdout"].(string); ok {
			decoded, err := base64.StdEncoding.DecodeString(stdout)
			if err == nil {
				result.Write(decoded)
			}
		}
		if stderr, ok := eventData["stderr"].(string); ok {
			decoded, err := base64.StdEncoding.DecodeString(stderr)
			if err == nil {
				result.Write(decoded)
			}
		}
	}
	return result.String()
}

// findSandboxHostIP finds the host IP (10.11.x.x) of a sandbox on the local node
// by matching the Firecracker process to its network namespace.
func findSandboxHostIP(sbxID string) string {
	// Find FC process by sandbox ID in its cgroup or socket name
	out, err := exec.Command("bash", "-c",
		fmt.Sprintf("pgrep -a firecracker | grep '%s' | head -1 | awk '{print $1}'", sbxID),
	).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return "(unknown - FC process not found)"
	}
	pid := strings.TrimSpace(string(out))

	// Find which ns-X this process is in
	out, err = exec.Command("bash", "-c",
		fmt.Sprintf(`NS_INODE=$(readlink /proc/%s/ns/net | grep -o '[0-9]*'); for ns in $(ip netns list | awk '{print $1}'); do if ip netns exec $ns readlink /proc/self/ns/net 2>/dev/null | grep -q "$NS_INODE"; then echo $ns; break; fi; done`, pid),
	).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return "(unknown - namespace not found)"
	}
	ns := strings.TrimSpace(string(out))

	// Extract slot index from ns-X, host IP is 10.11.0.X
	idx := strings.TrimPrefix(ns, "ns-")
	return fmt.Sprintf("10.11.0.%s", idx)
}

// findSandboxHostIPRemote finds host IP on box via SSH.
func findSandboxHostIPRemote(sbxID string) string {
	out, err := exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "root@10.99.0.1",
		fmt.Sprintf(`PID=$(pgrep -a firecracker | grep '%s' | head -1 | awk '{print $1}'); NS_INODE=$(readlink /proc/$PID/ns/net | grep -o '[0-9]*'); for ns in $(ip netns list | awk '{print $1}'); do if ip netns exec $ns readlink /proc/self/ns/net 2>/dev/null | grep -q "$NS_INODE"; then echo $ns; break; fi; done`, sbxID),
	).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return "(unknown)"
	}
	ns := strings.TrimSpace(string(out))
	idx := strings.TrimPrefix(ns, "ns-")
	return fmt.Sprintf("10.11.0.%s", idx)
}

func shellJoinArgs(cmd string, args []string) string {
	parts := []string{cmd}
	parts = append(parts, args...)
	var quoted []string
	for _, p := range parts {
		if strings.ContainsAny(p, " \t'\"\\$") {
			quoted = append(quoted, fmt.Sprintf("'%s'", strings.ReplaceAll(p, "'", "'\\''")))
		} else {
			quoted = append(quoted, p)
		}
	}
	return strings.Join(quoted, " ")
}

// --- snapshot transfer ---

func transferSnapshot(ctx context.Context, buildID string) error {
	tgtWg := net.ParseIP(*targetWgIP).String()
	sshOpt := "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"

	if *sourceStorageDir != "" && *targetStorageDir != "" {
		src := fmt.Sprintf("%s/%s/", *sourceStorageDir, buildID)
		dst := fmt.Sprintf("root@%s:%s/%s/", tgtWg, *targetStorageDir, buildID)
		if err := runCmd(ctx, "rsync", "-az", "--mkpath", "-e", sshOpt, src, dst); err != nil {
			return fmt.Errorf("rsync storage: %w", err)
		}
		return nil
	}

	cacheSrc := fmt.Sprintf("%s/%s/", *sourceCacheDir, buildID)
	cacheDst := fmt.Sprintf("root@%s:%s/%s/", tgtWg, *targetCacheDir, buildID)
	if err := runCmd(ctx, "rsync", "-az", "--mkpath", "-e", sshOpt, cacheSrc, cacheDst); err != nil {
		return fmt.Errorf("rsync cache: %w", err)
	}

	diffSrc := fmt.Sprintf("%s/", *sourceDiffCache)
	diffDst := fmt.Sprintf("root@%s:%s/", tgtWg, *targetDiffCache)
	if err := runCmd(ctx, "rsync", "-az", "--mkpath", "-e", sshOpt,
		"--include", fmt.Sprintf("%s*", buildID), "--exclude", "*",
		diffSrc, diffDst); err != nil {
		return fmt.Errorf("rsync diffs: %w", err)
	}
	return nil
}

func runCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- gRPC helpers ---

func countSandboxes(ctx context.Context, source, target orchestrator.SandboxServiceClient) (int, int) {
	var s, t int
	if r, err := source.List(ctx, &emptypb.Empty{}); err == nil {
		s = len(r.GetSandboxes())
	}
	if r, err := target.List(ctx, &emptypb.Empty{}); err == nil {
		t = len(r.GetSandboxes())
	}
	return s, t
}

func captureSandboxConfig(ctx context.Context, client orchestrator.SandboxServiceClient, sbxID string) *orchestrator.SandboxConfig {
	resp, err := client.List(ctx, &emptypb.Empty{})
	if err != nil {
		fatalf("list for config capture: %v", err)
	}
	for _, sb := range resp.GetSandboxes() {
		if sb.GetConfig().GetSandboxId() == sbxID {
			return proto.Clone(sb.GetConfig()).(*orchestrator.SandboxConfig)
		}
	}
	fatalf("sandbox %s not found for config capture", sbxID)
	return nil
}

func verifyOnNode(ctx context.Context, client orchestrator.SandboxServiceClient, sbxID, node string) {
	resp, err := client.List(ctx, &emptypb.Empty{})
	if err != nil {
		fatalf("list on %s: %v", node, err)
	}
	for _, sb := range resp.GetSandboxes() {
		if sb.GetConfig().GetSandboxId() == sbxID {
			return
		}
	}
	fatalf("sandbox %s not found on %s", sbxID, node)
}

// --- output ---

func logStep(start time.Time, format string, args ...interface{}) {
	elapsed := time.Since(start).Round(100 * time.Millisecond)
	fmt.Printf("[%6s] %s\n", elapsed, fmt.Sprintf(format, args...))
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
	os.Exit(1)
}
