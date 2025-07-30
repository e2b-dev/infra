package mount

import (
	"context"
	"fmt"
	"log"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// ExampleUsage demonstrates how to use the template mounting functionality
func ExampleUsage() {
	ctx := context.Background()
	logger := zap.L()

	// Initialize storage provider (this would typically be done in your main application setup)
	templateStorage, err := storage.GetTemplateStorageProvider(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to get template storage provider: %v", err)
	}

	// Initialize NBD device pool (this would typically be done in your main application setup)
	devicePool, err := nbd.NewPool(ctx, 64) // Max 64 NBD devices
	if err != nil {
		log.Fatalf("Failed to create NBD device pool: %v", err)
	}
	defer devicePool.Close(ctx)

	// Create mount manager
	mountManager := NewManager(logger, templateStorage, devicePool)
	defer mountManager.Close(ctx)

	// Example 1: Mount a template
	templateID := "my-template"
	buildID := "build-12345"
	mountPath := "/mnt/template-mount"

	mountInfo, err := mountManager.MountTemplate(ctx, templateID, buildID, mountPath)
	if err != nil {
		log.Fatalf("Failed to mount template: %v", err)
	}

	fmt.Printf("Template %s/%s mounted successfully at %s using device %s\n",
		mountInfo.TemplateID, mountInfo.BuildID, mountInfo.MountPath, mountInfo.DevicePath)

	// Now the template filesystem is accessible at /mnt/template-mount
	// You can read files, explore the filesystem, etc.

	// Example 2: List all mounts
	mounts := mountManager.ListMounts()
	fmt.Printf("Active mounts: %d\n", len(mounts))
	for _, mount := range mounts {
		fmt.Printf("  - %s/%s at %s\n", mount.TemplateID, mount.BuildID, mount.MountPath)
	}

	// Example 3: Unmount the template
	err = mountManager.UnmountTemplate(mountPath)
	if err != nil {
		log.Fatalf("Failed to unmount template: %v", err)
	}

	fmt.Printf("Template unmounted successfully from %s\n", mountPath)
}

// ExampleTemplateCommandUsage demonstrates how to use MOUNT/UNMOUNT commands in template building
func ExampleTemplateCommandUsage() {
	// This shows how you would use the MOUNT and UNMOUNT commands in a template configuration
	
	// Example template step configuration:
	fmt.Println(`
Template steps example:

steps:
  - type: MOUNT
    args: ["my-template", "build-12345", "/mnt/external-template"]
    
  - type: RUN
    args: ["cp -r /mnt/external-template/some-files /app/"]
    
  - type: RUN
    args: ["ls -la /mnt/external-template"]
    
  - type: UNMOUNT
    args: ["/mnt/external-template"]

This would:
1. Mount the template 'my-template' with build ID 'build-12345' to '/mnt/external-template'
2. Copy files from the mounted template to the current template being built
3. List files in the mounted template for verification
4. Unmount the template to clean up resources
`)
}

// ExampleProgrammaticUsage shows how to use the mount manager programmatically in other services
func ExampleProgrammaticUsage() {
	fmt.Println(`
Programmatic usage example:

package main

import (
    "context"
    "fmt"
    "io/ioutil"
    "path/filepath"
    
    "github.com/e2b-dev/infra/packages/orchestrator/internal/template/mount"
    // ... other imports
)

func main() {
    ctx := context.Background()
    
    // Initialize dependencies
    logger := zap.L()
    templateStorage, _ := storage.GetTemplateStorageProvider(ctx, nil)
    devicePool, _ := nbd.NewPool(ctx, 64)
    
    // Create mount manager
    mountManager := mount.NewManager(logger, templateStorage, devicePool)
    defer mountManager.Close(ctx)
    
    // Mount a template
    mountPath := "/mnt/my-template"
    mountInfo, err := mountManager.MountTemplate(ctx, "template-id", "build-id", mountPath)
    if err != nil {
        panic(err)
    }
    
    // Access files in the mounted template
    files, err := ioutil.ReadDir(mountPath)
    if err != nil {
        panic(err)
    }
    
    for _, file := range files {
        fmt.Printf("Found file: %s\n", file.Name())
        
        if file.IsDir() {
            continue
        }
        
        // Read file content
        content, err := ioutil.ReadFile(filepath.Join(mountPath, file.Name()))
        if err != nil {
            continue
        }
        
        fmt.Printf("File %s content: %s\n", file.Name(), string(content[:min(100, len(content))]))
    }
    
    // Unmount when done
    mountManager.UnmountTemplate(mountPath)
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
`)
}