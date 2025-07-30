package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/mount"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func main() {
	var (
		templateID = flag.String("template-id", "", "Template ID to mount")
		buildID    = flag.String("build-id", "", "Build ID to mount")
		mountPath  = flag.String("mount-path", "", "Path to mount the template")
		unmount    = flag.Bool("unmount", false, "Unmount the template")
		list       = flag.Bool("list", false, "List active mounts")
	)
	flag.Parse()

	// Initialize logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("Received shutdown signal, cleaning up...")
		cancel()
	}()

	// Initialize storage provider
	templateStorage, err := storage.GetTemplateStorageProvider(ctx, &limit.Limiter{})
	if err != nil {
		logger.Fatal("Failed to get template storage provider", zap.Error(err))
	}

	// Initialize NBD device pool
	devicePool, err := nbd.NewPool(ctx, 32) // Max 32 NBD devices
	if err != nil {
		logger.Fatal("Failed to create NBD device pool", zap.Error(err))
	}
	defer func() {
		if closeErr := devicePool.Close(ctx); closeErr != nil {
			logger.Error("Failed to close device pool", zap.Error(closeErr))
		}
	}()

	// Create mount manager
	mountManager := mount.NewManager(logger, templateStorage, devicePool)
	defer func() {
		if closeErr := mountManager.Close(ctx); closeErr != nil {
			logger.Error("Failed to close mount manager", zap.Error(closeErr))
		}
	}()

	switch {
	case *list:
		listMounts(mountManager)
	case *unmount:
		if *mountPath == "" {
			fmt.Fprintf(os.Stderr, "mount-path is required for unmount operation\n")
			os.Exit(1)
		}
		unmountTemplate(mountManager, *mountPath, logger)
	default:
		if *templateID == "" || *buildID == "" || *mountPath == "" {
			fmt.Fprintf(os.Stderr, "template-id, build-id, and mount-path are required\n")
			flag.Usage()
			os.Exit(1)
		}
		mountTemplate(ctx, mountManager, *templateID, *buildID, *mountPath, logger)
	}
}

func mountTemplate(ctx context.Context, manager *mount.Manager, templateID, buildID, mountPath string, logger *zap.Logger) {
	logger.Info("Mounting template",
		zap.String("template_id", templateID),
		zap.String("build_id", buildID),
		zap.String("mount_path", mountPath))

	mountInfo, err := manager.MountTemplate(ctx, templateID, buildID, mountPath)
	if err != nil {
		logger.Fatal("Failed to mount template", zap.Error(err))
	}

	fmt.Printf("✅ Template mounted successfully!\n")
	fmt.Printf("  Template: %s/%s\n", mountInfo.TemplateID, mountInfo.BuildID)
	fmt.Printf("  Mount Path: %s\n", mountInfo.MountPath)
	fmt.Printf("  Device: %s\n", mountInfo.DevicePath)
	fmt.Printf("\nThe template filesystem is now accessible at: %s\n", mountPath)
	fmt.Printf("To unmount, run: %s -unmount -mount-path=%s\n", os.Args[0], mountPath)
}

func unmountTemplate(manager *mount.Manager, mountPath string, logger *zap.Logger) {
	logger.Info("Unmounting template", zap.String("mount_path", mountPath))

	err := manager.UnmountTemplate(mountPath)
	if err != nil {
		logger.Fatal("Failed to unmount template", zap.Error(err))
	}

	fmt.Printf("✅ Template unmounted successfully from: %s\n", mountPath)
}

func listMounts(manager *mount.Manager) {
	mounts := manager.ListMounts()
	
	if len(mounts) == 0 {
		fmt.Println("No active mounts")
		return
	}

	fmt.Printf("Active mounts (%d):\n", len(mounts))
	for i, mountInfo := range mounts {
		fmt.Printf("  %d. Template: %s/%s\n", i+1, mountInfo.TemplateID, mountInfo.BuildID)
		fmt.Printf("     Mount Path: %s\n", mountInfo.MountPath)
		fmt.Printf("     Device: %s\n", mountInfo.DevicePath)
		fmt.Printf("     Temp File: %s\n", mountInfo.TempFilePath)
		fmt.Println()
	}
}