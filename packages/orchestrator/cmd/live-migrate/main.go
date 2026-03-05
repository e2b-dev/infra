package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const (
	defaultKernel = "vmlinux-6.1.158"
	defaultFC     = "v1.12.1_a41d3fb"
)

func main() {
	sandboxID := flag.String("sandbox-id", "", "sandbox to migrate (required unless -test)")
	sourceAddr := flag.String("source", "", "source orchestrator host:port")
	destAddr := flag.String("dest", "", "destination orchestrator host:port")
	timeout := flag.Duration("timeout", 5*time.Minute, "timeout")

	testMode := flag.Bool("test", false, "end-to-end test: create, migrate, verify")
	buildID := flag.String("build-id", "", "build ID (required for -test)")
	kernel := flag.String("kernel", defaultKernel, "kernel version")
	fcVer := flag.String("fc-version", defaultFC, "firecracker version")
	ramMB := flag.Int64("ram-mb", 512, "RAM in MB")

	flag.Parse()

	if *sourceAddr == "" || *destAddr == "" {
		flag.Usage()
		os.Exit(1)
	}
	if !*testMode && *sandboxID == "" {
		fmt.Fprintln(os.Stderr, "-sandbox-id required unless -test")
		os.Exit(1)
	}
	if *testMode && *buildID == "" {
		fmt.Fprintln(os.Stderr, "-build-id required for -test")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; cancel() }()

	var err error
	if *testMode {
		err = runTest(ctx, *sourceAddr, *destAddr, *buildID, *kernel, *fcVer, *ramMB)
	} else {
		err = runMigrate(ctx, *sandboxID, *sourceAddr, *destAddr)
	}
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func dial(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func runMigrate(ctx context.Context, sandboxID, src, dst string) error {
	fmt.Printf("Migrating %s: %s -> %s\n", sandboxID, src, dst)
	conn, err := dial(src)
	if err != nil {
		return err
	}
	defer conn.Close()

	t0 := time.Now()
	resp, err := orchestrator.NewMigrationServiceClient(conn).InitMigration(ctx, &orchestrator.MigrationInitRequest{
		SandboxId:   sandboxID,
		DestAddress: dst,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Done in %s (build: %s)\n", time.Since(t0), resp.GetBuildId())

	return nil
}

func runTest(ctx context.Context, src, dst, buildID, kernel, fcVer string, ramMB int64) error {
	sameNode := src == dst
	fmt.Println("=== Live Migration Test ===")
	fmt.Printf("  Source: %s  Dest: %s\n", src, dst)
	fmt.Printf("  Build: %s  Kernel: %s  FC: %s  RAM: %dMB\n", buildID, kernel, fcVer, ramMB)
	if sameNode {
		fmt.Println("  Mode: same-node")
	} else {
		fmt.Println("  Mode: cross-node (P2P)")
	}
	fmt.Println()

	srcConn, err := dial(src)
	if err != nil {
		return err
	}
	defer srcConn.Close()
	srcSbx := orchestrator.NewSandboxServiceClient(srcConn)
	srcMig := orchestrator.NewMigrationServiceClient(srcConn)

	sandboxID := fmt.Sprintf("mig%x", time.Now().UnixNano())
	now := time.Now()
	accessToken := "test-token"
	fmt.Printf("[1/4] Creating sandbox %s...\n", sandboxID)

	_, err = srcSbx.Create(ctx, &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			TemplateId:         buildID,
			BuildId:            buildID,
			KernelVersion:      kernel,
			FirecrackerVersion: fcVer,
			SandboxId:          sandboxID,
			EnvVars:            map[string]string{},
			Vcpu:               1,
			RamMb:              ramMB,
			TotalDiskSizeMb:    512,
			TeamId:             "test",
			MaxSandboxLength:   1,
			Snapshot:           true,
			BaseTemplateId:     buildID,
			ExecutionId:        fmt.Sprintf("exec%x", now.UnixNano()),
			EnvdVersion:        "1.0.0",
			EnvdAccessToken:    &accessToken,
		},
		StartTime: timestamppb.New(now),
		EndTime:   timestamppb.New(now.Add(1 * time.Hour)),
	})
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	fmt.Println("       OK")

	fmt.Println("[2/4] Migrating...")
	t0 := time.Now()
	resp, err := srcMig.InitMigration(ctx, &orchestrator.MigrationInitRequest{
		SandboxId:   sandboxID,
		DestAddress: dst,
	})
	if err != nil {
		srcSbx.Delete(ctx, &orchestrator.SandboxDeleteRequest{SandboxId: sandboxID})

		return fmt.Errorf("migrate: %w", err)
	}
	elapsed := time.Since(t0)
	fmt.Printf("       Done in %s (build: %s)\n", elapsed, resp.GetBuildId())

	fmt.Println("[3/4] Verifying on destination...")
	dstConn, err := dial(dst)
	if err != nil {
		return err
	}
	defer dstConn.Close()
	dstSbx := orchestrator.NewSandboxServiceClient(dstConn)
	if !sandboxExists(ctx, dstSbx, sandboxID) {
		return fmt.Errorf("not found on destination")
	}
	fmt.Println("       OK")

	fmt.Println("[4/4] Verifying removed from source...")
	if sandboxExists(ctx, srcSbx, sandboxID) {
		fmt.Println("       Note: still listed (async cleanup)")
	} else {
		fmt.Println("       OK: removed")
	}

	fmt.Printf("\n=== PASSED === migrated in %s\n", elapsed)
	fmt.Printf("SANDBOX_ID=%s\n", sandboxID)

	return nil
}

func sandboxExists(ctx context.Context, c orchestrator.SandboxServiceClient, id string) bool {
	list, err := c.List(ctx, &emptypb.Empty{})
	if err != nil {
		return false
	}
	for _, s := range list.GetSandboxes() {
		if s.GetConfig().GetSandboxId() == id {
			return true
		}
	}

	return false
}
