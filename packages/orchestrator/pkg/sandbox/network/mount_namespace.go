//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	mountNamespaceNamePrefix         = "mnt-"
	mountNamespaceTemplateNamePrefix = "template-"
)

type mountNamespace struct {
	Name    string
	Path    string
	PIDPath string
}

func (m *mountNamespace) Close() error {
	if m == nil {
		return nil
	}

	return cleanupMountNamespaceHolder(m.Path, m.PIDPath)
}

type mountNamespaceFactory struct {
	template      *mountNamespace
	templateMu    sync.Mutex
	keepPaths     []string
	pruneTemplate bool

	done     chan struct{}
	doneOnce sync.Once
	seq      atomic.Uint64
}

func newMountNamespaceFactory(templateKeepPaths string, pruneTemplate bool) *mountNamespaceFactory {
	return &mountNamespaceFactory{
		done:          make(chan struct{}),
		keepPaths:     parseMountNamespaceKeepPaths(templateKeepPaths),
		pruneTemplate: pruneTemplate,
	}
}

func (p *mountNamespaceFactory) Create(ctx context.Context) (*mountNamespace, error) {
	select {
	case <-p.done:
		return nil, ErrClosed
	default:
	}

	templateMntNS, err := p.openTemplate(ctx)
	if err != nil {
		return nil, err
	}
	defer templateMntNS.Close()

	select {
	case <-p.done:
		return nil, ErrClosed
	default:
	}

	return p.create(ctx, templateMntNS)
}

func (p *mountNamespaceFactory) Close(ctx context.Context) error {
	p.doneOnce.Do(func() {
		close(p.done)
	})

	var errs []error
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	p.templateMu.Lock()
	defer p.templateMu.Unlock()

	if p.template != nil {
		if err := p.template.Close(); err != nil {
			errs = append(errs, err)
		}
		p.template = nil
	}

	return errors.Join(errs...)
}

func (p *mountNamespaceFactory) openTemplate(ctx context.Context) (*os.File, error) {
	p.templateMu.Lock()
	defer p.templateMu.Unlock()

	select {
	case <-p.done:
		return nil, ErrClosed
	default:
	}

	if p.template == nil {
		template, err := p.createTemplate(ctx)
		if err != nil {
			return nil, err
		}

		p.template = template
	}

	templateMntNS, err := os.Open(p.template.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to open template mount namespace %q: %w", p.template.Path, err)
	}

	return templateMntNS, nil
}

func (p *mountNamespaceFactory) createTemplate(ctx context.Context) (*mountNamespace, error) {
	name := fmt.Sprintf("%s%d", mountNamespaceTemplateNamePrefix, os.Getpid())
	template := &mountNamespace{
		Name:    name,
		Path:    filepath.Join(mountNamespacesDir, name),
		PIDPath: filepath.Join(mountNamespacesDir, name+".pid"),
	}

	if err := os.MkdirAll(mountNamespacesDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create mount namespace directory: %w", err)
	}

	_ = template.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return nil, fmt.Errorf("failed to unshare fs attributes before creating template mount namespace: %w", err)
	}

	hostMntNS, err := openCurrentMountNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed to open host mount namespace: %w", err)
	}
	defer hostMntNS.Close()

	restoreHostNS := func() {
		if err := unix.Setns(int(hostMntNS.Fd()), unix.CLONE_NEWNS); err != nil {
			logger.L().Error(ctx, "error resetting mount namespace back to the host namespace", zap.Error(err))
		}
	}

	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return nil, fmt.Errorf("failed to unshare template mount namespace: %w", err)
	}
	defer restoreHostNS()

	if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		return nil, fmt.Errorf("failed to make template mount namespace private: %w", err)
	}

	if p.pruneTemplate {
		if err := pruneMountTree(p.keepPaths); err != nil {
			return nil, fmt.Errorf("failed to prune template mount namespace: %w", err)
		}
	}

	holder, err := startMountNamespaceHolder()
	if err != nil {
		return nil, fmt.Errorf("failed to start template mount namespace holder: %w", err)
	}

	holderPID := holder.Process.Pid
	holderNSPath := fmt.Sprintf("/proc/%d/ns/mnt", holderPID)
	cleanupHolder := func() {
		killAndWait(holderPID, holder.Process)
	}

	if err := unix.Setns(int(hostMntNS.Fd()), unix.CLONE_NEWNS); err != nil {
		cleanupHolder()
		return nil, fmt.Errorf("failed to restore host mount namespace before recording template holder: %w", err)
	}

	if err := os.Symlink(holderNSPath, template.Path); err != nil {
		cleanupHolder()
		return nil, fmt.Errorf("failed to symlink template mount namespace %q -> %q: %w", template.Path, holderNSPath, err)
	}

	if err := os.WriteFile(template.PIDPath, []byte(strconv.Itoa(holderPID)+"\n"), 0o644); err != nil {
		_ = os.Remove(template.Path)
		cleanupHolder()
		return nil, fmt.Errorf("failed to write template mount namespace holder pid file %q: %w", template.PIDPath, err)
	}

	logger.L().Info(ctx, "[mount namespace factory]: created template mount namespace",
		zap.String("path", template.Path),
		zap.Strings("keep_paths", p.keepPaths),
		zap.Bool("pruned", p.pruneTemplate),
	)

	return template, nil
}

func (p *mountNamespaceFactory) create(ctx context.Context, templateMntNS *os.File) (*mountNamespace, error) {
	id := p.seq.Add(1)
	name := fmt.Sprintf("%s%d-%d", mountNamespaceNamePrefix, os.Getpid(), id)
	mountNS := &mountNamespace{
		Name:    name,
		Path:    filepath.Join(mountNamespacesDir, name),
		PIDPath: filepath.Join(mountNamespacesDir, name+".pid"),
	}

	if err := os.MkdirAll(mountNamespacesDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create mount namespace directory: %w", err)
	}

	_ = mountNS.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return nil, fmt.Errorf("failed to unshare fs attributes before creating mount namespace: %w", err)
	}

	hostMntNS, err := openCurrentMountNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed to open host mount namespace: %w", err)
	}
	defer hostMntNS.Close()

	restoreHostNS := func() {
		if err := unix.Setns(int(hostMntNS.Fd()), unix.CLONE_NEWNS); err != nil {
			logger.L().Error(ctx, "error resetting mount namespace back to the host namespace", zap.Error(err))
		}
	}

	if err := unix.Setns(int(templateMntNS.Fd()), unix.CLONE_NEWNS); err != nil {
		return nil, fmt.Errorf("failed to enter template mount namespace: %w", err)
	}
	defer restoreHostNS()

	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return nil, fmt.Errorf("failed to unshare mount namespace: %w", err)
	}

	if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		return nil, fmt.Errorf("failed to make mount namespace private: %w", err)
	}

	holder, err := startMountNamespaceHolder()
	if err != nil {
		return nil, fmt.Errorf("failed to start mount namespace holder: %w", err)
	}

	holderPID := holder.Process.Pid
	holderNSPath := fmt.Sprintf("/proc/%d/ns/mnt", holderPID)

	cleanupHolder := func() {
		killAndWait(holderPID, holder.Process)
	}

	if err := unix.Setns(int(hostMntNS.Fd()), unix.CLONE_NEWNS); err != nil {
		cleanupHolder()
		return nil, fmt.Errorf("failed to restore host mount namespace before recording holder: %w", err)
	}

	if err := os.Symlink(holderNSPath, mountNS.Path); err != nil {
		cleanupHolder()
		return nil, fmt.Errorf("failed to symlink mount namespace %q -> %q: %w", mountNS.Path, holderNSPath, err)
	}

	if err := os.WriteFile(mountNS.PIDPath, []byte(strconv.Itoa(holderPID)+"\n"), 0o644); err != nil {
		_ = os.Remove(mountNS.Path)
		cleanupHolder()
		return nil, fmt.Errorf("failed to write mount namespace holder pid file %q: %w", mountNS.PIDPath, err)
	}

	return mountNS, nil
}

func openCurrentMountNamespace() (*os.File, error) {
	currentMntNSPath := fmt.Sprintf("/proc/%d/task/%d/ns/mnt", os.Getpid(), unix.Gettid())

	return os.Open(currentMntNSPath)
}

func startMountNamespaceHolder() (*exec.Cmd, error) {
	holder := exec.Command("sleep", "2147483647")
	holder.Dir = "/"

	return holder, holder.Start()
}

func killAndWait(pid int, process *os.Process) {
	if process != nil {
		_ = process.Kill()
	} else {
		_ = unix.Kill(pid, unix.SIGKILL)
	}

	var status unix.WaitStatus
	_, _ = unix.Wait4(pid, &status, 0, nil)
}

func parseMountNamespaceKeepPaths(raw string) []string {
	seen := map[string]struct{}{"/": {}}
	paths := []string{"/"}

	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		item = filepath.Clean(item)
		if !filepath.IsAbs(item) {
			item = string(filepath.Separator) + item
		}

		if _, ok := seen[item]; ok {
			continue
		}

		seen[item] = struct{}{}
		paths = append(paths, item)
	}

	return paths
}

func pruneMountTree(keepPaths []string) error {
	mountPoints, err := readMountInfoMountPoints("/proc/self/mountinfo")
	if err != nil {
		return err
	}

	sort.SliceStable(mountPoints, func(i, j int) bool {
		if mountDepth(mountPoints[i]) == mountDepth(mountPoints[j]) {
			return len(mountPoints[i]) > len(mountPoints[j])
		}

		return mountDepth(mountPoints[i]) > mountDepth(mountPoints[j])
	})

	var errs []error
	for _, mountPoint := range mountPoints {
		if mountPoint == "/" || shouldKeepMountPoint(mountPoint, keepPaths) {
			continue
		}

		if err := unix.Unmount(mountPoint, unix.MNT_DETACH); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
			errs = append(errs, fmt.Errorf("failed to unmount %q from template mount namespace: %w", mountPoint, err))
		}
	}

	return errors.Join(errs...)
}

func readMountInfoMountPoints(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return parseMountInfoMountPoints(string(data))
}

func parseMountInfoMountPoints(data string) ([]string, error) {
	var mountPoints []string

	for _, line := range strings.Split(data, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 5 {
			return nil, fmt.Errorf("invalid mountinfo line %q", line)
		}

		mountPoint, err := unescapeMountInfoPath(fields[4])
		if err != nil {
			return nil, fmt.Errorf("invalid mountinfo mount point %q: %w", fields[4], err)
		}

		mountPoints = append(mountPoints, filepath.Clean(mountPoint))
	}

	return mountPoints, nil
}

func unescapeMountInfoPath(value string) (string, error) {
	var b strings.Builder

	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			b.WriteByte(value[i])
			continue
		}

		if i+3 >= len(value) {
			return "", fmt.Errorf("truncated escape")
		}

		escaped, err := strconv.ParseUint(value[i+1:i+4], 8, 8)
		if err != nil {
			return "", err
		}

		b.WriteByte(byte(escaped))
		i += 3
	}

	return b.String(), nil
}

func shouldKeepMountPoint(mountPoint string, keepPaths []string) bool {
	mountPoint = filepath.Clean(mountPoint)
	for _, keepPath := range keepPaths {
		keepPath = filepath.Clean(keepPath)
		if keepPath == mountPoint {
			return true
		}

		if isPathAncestor(mountPoint, keepPath) {
			return true
		}
	}

	return false
}

func isPathAncestor(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)

	if parent == "/" {
		return true
	}

	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

func mountDepth(path string) int {
	path = filepath.Clean(path)
	if path == "/" {
		return 0
	}

	return strings.Count(path, string(filepath.Separator))
}

func cleanupMountNamespaceHolder(path, pidPath string) error {
	var errs []error

	if pidBytes, err := os.ReadFile(pidPath); err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if parseErr != nil {
			errs = append(errs, fmt.Errorf("error parsing mount namespace holder pid file %q: %w", pidPath, parseErr))
		} else if pid > 0 {
			killAndWait(pid, nil)
		}
	} else if !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("error reading mount namespace holder pid file %q: %w", pidPath, err))
	}

	// Compatible cleanup for older failed versions that may have created a bind mount.
	_ = unix.Unmount(path, unix.MNT_DETACH)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("error removing mount namespace symlink %q: %w", path, err))
	}

	if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("error removing mount namespace holder pid file %q: %w", pidPath, err))
	}

	return errors.Join(errs...)
}
