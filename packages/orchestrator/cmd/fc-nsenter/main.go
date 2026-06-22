//go:build linux

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

const netNamespacesEtcDir = "/etc/netns"

type options struct {
	mountNSPath    string
	netNSPath      string
	rootfsMountDir string
	rootfsSource   string
	rootfsLink     string
	kernelDir      string
	kernelSource   string
	kernelLink     string
	mountKernelDir bool
	firecracker    string
	apiSock        string
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	opts := parseOptions()
	if err := opts.validate(); err != nil {
		return err
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := enterNamespaces(opts.mountNSPath, opts.netNSPath); err != nil {
		return err
	}

	if err := prepareNetworkMounts(opts.netNSPath); err != nil {
		return err
	}

	if err := prepareFirecrackerMounts(opts); err != nil {
		return err
	}

	argv := []string{opts.firecracker, "--api-sock", opts.apiSock}
	if err := unix.Exec(opts.firecracker, argv, os.Environ()); err != nil {
		return fmt.Errorf("failed to exec firecracker %q: %w", opts.firecracker, err)
	}

	return nil
}

func parseOptions() options {
	var opts options

	flag.StringVar(&opts.mountNSPath, "mount-ns", "", "mount namespace path")
	flag.StringVar(&opts.netNSPath, "net-ns", "", "network namespace path")
	flag.StringVar(&opts.rootfsMountDir, "rootfs-mount-dir", "", "rootfs mount directory")
	flag.StringVar(&opts.rootfsSource, "rootfs-source", "", "host rootfs source path")
	flag.StringVar(&opts.rootfsLink, "rootfs-link", "", "rootfs symlink path in the mount namespace")
	flag.StringVar(&opts.kernelDir, "kernel-dir", "", "kernel directory in the mount namespace")
	flag.StringVar(&opts.kernelSource, "kernel-source", "", "host kernel source path")
	flag.StringVar(&opts.kernelLink, "kernel-link", "", "kernel symlink path in the mount namespace")
	flag.BoolVar(&opts.mountKernelDir, "mount-kernel-dir", false, "mount a separate tmpfs for the kernel directory")
	flag.StringVar(&opts.firecracker, "firecracker", "", "firecracker binary path")
	flag.StringVar(&opts.apiSock, "api-sock", "", "firecracker API socket path")
	flag.Parse()

	return opts
}

func (o options) validate() error {
	required := map[string]string{
		"mount-ns":         o.mountNSPath,
		"net-ns":           o.netNSPath,
		"rootfs-mount-dir": o.rootfsMountDir,
		"rootfs-source":    o.rootfsSource,
		"rootfs-link":      o.rootfsLink,
		"kernel-dir":       o.kernelDir,
		"kernel-source":    o.kernelSource,
		"kernel-link":      o.kernelLink,
		"firecracker":      o.firecracker,
		"api-sock":         o.apiSock,
	}

	for name, value := range required {
		if value == "" {
			return fmt.Errorf("--%s is required", name)
		}
	}

	return nil
}

func enterNamespaces(mountNSPath, netNSPath string) error {
	mountNS, err := os.Open(mountNSPath)
	if err != nil {
		return fmt.Errorf("failed to open mount namespace %q: %w", mountNSPath, err)
	}
	defer mountNS.Close()

	netNS, err := os.Open(netNSPath)
	if err != nil {
		return fmt.Errorf("failed to open network namespace %q: %w", netNSPath, err)
	}
	defer netNS.Close()

	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return fmt.Errorf("failed to unshare fs attributes before entering mount namespace %q: %w", mountNSPath, err)
	}

	if err := unix.Setns(int(mountNS.Fd()), unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("failed to enter mount namespace %q: %w", mountNSPath, err)
	}

	if err := unix.Setns(int(netNS.Fd()), unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("failed to enter network namespace %q: %w", netNSPath, err)
	}

	return nil
}

func prepareNetworkMounts(netNSPath string) error {
	namespaceID := filepath.Base(netNSPath)
	if namespaceID == "." || namespaceID == string(filepath.Separator) || namespaceID == "" {
		return fmt.Errorf("failed to derive network namespace name from %q", netNSPath)
	}

	if err := mountNetworkSysfs(namespaceID); err != nil {
		return err
	}

	if err := bindEtc(namespaceID); err != nil {
		return err
	}

	return nil
}

func mountNetworkSysfs(namespaceID string) error {
	var mountFlags uintptr

	if err := unix.Unmount("/sys", unix.MNT_DETACH); err != nil {
		var stat unix.Statfs_t
		if statErr := unix.Statfs("/sys", &stat); statErr == nil && stat.Flags&unix.ST_RDONLY != 0 {
			mountFlags = unix.MS_RDONLY
		}
	}

	if err := unix.Mount(namespaceID, "/sys", "sysfs", mountFlags, ""); err != nil {
		return fmt.Errorf("failed to mount sysfs for namespace %q: %w", namespaceID, err)
	}

	return nil
}

func bindEtc(namespaceID string) error {
	etcNetnsPath := filepath.Join(netNamespacesEtcDir, namespaceID)

	entries, err := os.ReadDir(etcNetnsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("failed to read %q: %w", etcNetnsPath, err)
	}

	var errs []error
	for _, entry := range entries {
		source := filepath.Join(etcNetnsPath, entry.Name())
		target := filepath.Join("/etc", entry.Name())
		if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
			errs = append(errs, fmt.Errorf("failed to bind network namespace etc file %q -> %q: %w", source, target, err))
		}
	}

	return errors.Join(errs...)
}

func prepareFirecrackerMounts(o options) error {
	detachUnmount(o.kernelDir)
	if o.rootfsMountDir != o.kernelDir {
		detachUnmount(o.rootfsMountDir)
	}

	if err := mountTmpfs(o.rootfsMountDir); err != nil {
		return fmt.Errorf("failed to mount rootfs tmpfs %q: %w", o.rootfsMountDir, err)
	}

	if err := symlinkForce(o.rootfsSource, o.rootfsLink); err != nil {
		return fmt.Errorf("failed to link rootfs %q -> %q: %w", o.rootfsLink, o.rootfsSource, err)
	}

	if o.mountKernelDir {
		if err := mountTmpfs(o.kernelDir); err != nil {
			return fmt.Errorf("failed to mount kernel tmpfs %q: %w", o.kernelDir, err)
		}
	} else if err := os.MkdirAll(o.kernelDir, 0o755); err != nil {
		return fmt.Errorf("failed to create kernel directory %q: %w", o.kernelDir, err)
	}

	if err := symlinkForce(o.kernelSource, o.kernelLink); err != nil {
		return fmt.Errorf("failed to link kernel %q -> %q: %w", o.kernelLink, o.kernelSource, err)
	}

	return nil
}

func mountTmpfs(target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}

	return unix.Mount("tmpfs", target, "tmpfs", 0, "")
}

func detachUnmount(target string) {
	_ = unix.Unmount(target, unix.MNT_DETACH)
}

func symlinkForce(source, target string) error {
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}

	return os.Symlink(source, target)
}
