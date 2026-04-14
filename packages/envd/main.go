package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"connectrpc.com/authn"
	connectcors "connectrpc.com/cors"
	"github.com/go-chi/chi/v5"
	"github.com/rs/cors"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/envd/internal/api"
	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	publicport "github.com/e2b-dev/infra/packages/envd/internal/port"
	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	"github.com/e2b-dev/infra/packages/envd/internal/services/fssnapshot"
	filesystemRpc "github.com/e2b-dev/infra/packages/envd/internal/services/filesystem"
	processRpc "github.com/e2b-dev/infra/packages/envd/internal/services/process"
	processSpec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
	"github.com/e2b-dev/infra/packages/envd/pkg"
)

const (
	// Downstream timeout should be greater than upstream (in orchestrator proxy).
	idleTimeout = 640 * time.Second
	maxAge      = 2 * time.Hour

	defaultPort = 49983

	portScannerInterval = 1000 * time.Millisecond

	// This is the default user used in the container if not specified otherwise.
	// It should be always overridden by the user in /init when building the template.
	defaultUser = "root"

	kilobyte = 1024
	megabyte = 1024 * kilobyte
)

var (
	commitSHA string

	isNotFC bool
	port    int64

	versionFlag  bool
	commitFlag   bool
	startCmdFlag string
	cgroupRoot   string
	fsMode       string
)

func parseFlags() {
	flag.BoolVar(
		&isNotFC,
		"isnotfc",
		false,
		"isNotFCmode prints all logs to stdout",
	)

	flag.BoolVar(
		&versionFlag,
		"version",
		false,
		"print envd version",
	)

	flag.BoolVar(
		&commitFlag,
		"commit",
		false,
		"print envd source commit",
	)

	flag.Int64Var(
		&port,
		"port",
		defaultPort,
		"a port on which the daemon should run",
	)

	flag.StringVar(
		&startCmdFlag,
		"cmd",
		"",
		"a command to run on the daemon start",
	)

	flag.StringVar(
		&cgroupRoot,
		"cgroup-root",
		"/sys/fs/cgroup",
		"cgroup root directory",
	)

	flag.StringVar(
		&fsMode,
		"fs-mode",
		"",
		"FS snapshot mode: 'hidden-base' runs as PID 1 from tmpfs with ext4 unmounted",
	)

	flag.Parse()
}

func withCORS(h http.Handler) http.Handler {
	middleware := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{
			http.MethodHead,
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
		},
		AllowedHeaders: []string{"*"},
		ExposedHeaders: append(
			connectcors.ExposedHeaders(),
			"Location",
			"Cache-Control",
			"X-Content-Type-Options",
		),
		MaxAge: int(maxAge.Seconds()),
	})

	return middleware.Handler(h)
}

func main() {
	parseFlags()

	if versionFlag {
		fmt.Printf("%s\n", pkg.Version)

		return
	}

	if commitFlag {
		fmt.Printf("%s\n", commitSHA)

		return
	}

	if fsMode == "hidden-base" || fssnapshot.IsHiddenBaseMode() {
		runHiddenBaseMode()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := os.MkdirAll(host.E2BRunDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating E2B run directory: %v\n", err)
	}

	defaults := &execcontext.Defaults{
		User:    defaultUser,
		EnvVars: utils.NewMap[string, string](),
	}
	isFCBoolStr := strconv.FormatBool(!isNotFC)
	defaults.EnvVars.Store("E2B_SANDBOX", isFCBoolStr)
	if err := os.WriteFile(filepath.Join(host.E2BRunDir, ".E2B_SANDBOX"), []byte(isFCBoolStr), 0o444); err != nil {
		fmt.Fprintf(os.Stderr, "error writing sandbox file: %v\n", err)
	}

	mmdsChan := make(chan *host.MMDSOpts, 1)
	defer close(mmdsChan)
	if !isNotFC {
		go host.PollForMMDSOpts(ctx, mmdsChan, defaults.EnvVars)
	}

	l := logs.NewLogger(ctx, isNotFC, mmdsChan)

	m := chi.NewRouter()

	envLogger := l.With().Str("logger", "envd").Logger()
	fsLogger := l.With().Str("logger", "filesystem").Logger()
	filesystemRpc.Handle(m, &fsLogger, defaults)

	cgroupManager := createCgroupManager()
	defer func() {
		err := cgroupManager.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to close cgroup manager: %v\n", err)
		}
	}()

	processLogger := l.With().Str("logger", "process").Logger()
	processService := processRpc.Handle(m, &processLogger, defaults, cgroupManager)

	m.Post("/fs-snapshot/prepare-base", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		if err := fssnapshot.PrepareBase("/usr/bin/envd"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Flush the response before switch-root kills this process.
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})

	m.Post("/fs-snapshot/sync", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		fssnapshot.Sync()
		w.WriteHeader(http.StatusOK)
	})

	service := api.New(&envLogger, defaults, mmdsChan, isNotFC)
	handler := api.HandlerFromMux(service, m)
	middleware := authn.NewMiddleware(permissions.AuthenticateUsername)

	s := &http.Server{
		Handler: withCORS(
			service.WithAuthorization(
				middleware.Wrap(handler),
			),
		),
		Addr: fmt.Sprintf("0.0.0.0:%d", port),
		// We remove the timeouts as the connection is terminated by closing of the sandbox and keepalive close.
		ReadTimeout:  0,
		WriteTimeout: 0,
		IdleTimeout:  idleTimeout,
	}

	// TODO: Not used anymore in template build, replaced by direct envd command call.
	if startCmdFlag != "" {
		tag := "startCmd"
		cwd := "/home/user"
		user, err := permissions.GetUser("root")
		if err != nil {
			log.Fatalf("error getting user: %v", err) //nolint:gocritic // probably fine to bail if we're done?
		}

		if err = processService.InitializeStartProcess(ctx, user, &processSpec.StartRequest{
			Tag: &tag,
			Process: &processSpec.ProcessConfig{
				Envs: make(map[string]string),
				Cmd:  "/bin/bash",
				Args: []string{"-l", "-c", startCmdFlag},
				Cwd:  &cwd,
			},
		}); err != nil {
			log.Fatalf("error starting process: %v", err)
		}
	}

	// Bind all open ports on 127.0.0.1 and localhost to the eth0 interface
	portScanner := publicport.NewScanner(portScannerInterval)
	defer portScanner.Destroy()

	portLogger := l.With().Str("logger", "port-forwarder").Logger()
	portForwarder := publicport.NewForwarder(&portLogger, portScanner, cgroupManager)
	go portForwarder.StartForwarding(ctx)

	go portScanner.ScanAndBroadcast()

	err := s.ListenAndServe()
	if err != nil {
		log.Fatalf("error starting server: %v", err)
	}
}

// runHiddenBaseMode runs envd as PID 1 from tmpfs after systemctl switch-root.
// It unmounts the old ext4 root and serves a minimal HTTP server with only
// health check and fs-snapshot/resume endpoints.
func runHiddenBaseMode() {
	fmt.Println("envd: hidden-base mode starting")

	if err := mountPseudoFS(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to mount pseudo-filesystems: %v\n", err)
	}

	if err := fssnapshot.UnmountOldRoot(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to unmount old root: %v\n", err)
	}
	fmt.Println("envd: old root unmounted, ext4 is released")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /fs-snapshot/resume", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		if err := fssnapshot.MountAndPivot(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	fmt.Printf("envd: hidden-base server listening on %s\n", addr)

	s := &http.Server{
		Handler: mux,
		Addr:    addr,
	}

	if err := s.ListenAndServe(); err != nil {
		log.Fatalf("hidden-base server error: %v", err)
	}
}

// mountPseudoFS mounts /proc, /sys, /dev if not already mounted.
// Needed after switch-root when running as PID 1 from tmpfs.
func mountPseudoFS() error {
	mounts := []struct {
		source string
		target string
		fstype string
		flags  uintptr
	}{
		{"proc", "/proc", "proc", 0},
		{"sysfs", "/sys", "sysfs", 0},
		{"devtmpfs", "/dev", "devtmpfs", 0},
	}

	for _, m := range mounts {
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", m.target, err)
		}
		if isMounted(m.target) {
			continue
		}
		if err := unix.Mount(m.source, m.target, m.fstype, m.flags, ""); err != nil {
			return fmt.Errorf("mount %s on %s: %w", m.fstype, m.target, err)
		}
	}

	return nil
}

func isMounted(path string) bool {
	var st1, st2 unix.Stat_t
	if err := unix.Stat(path, &st1); err != nil {
		return false
	}
	parent := filepath.Dir(path)
	if err := unix.Stat(parent, &st2); err != nil {
		return false
	}

	return st1.Dev != st2.Dev
}

func createCgroupManager() (m cgroups.Manager) {
	defer func() {
		if m == nil {
			fmt.Fprintf(os.Stderr, "falling back to no-op cgroup manager\n")
			m = cgroups.NewNoopManager()
		}
	}()

	metrics, err := host.GetMetrics()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to calculate host metrics: %v\n", err)

		return nil
	}

	// try to keep 1/8 of the memory free, but no more than 128 MB
	maxMemoryReserved := min(metrics.MemTotal/8, uint64(128)*megabyte)
	memoryMax := metrics.MemTotal - maxMemoryReserved
	memoryHigh := memoryMax // same as memory.max — OOM-kill immediately when throttling can't reclaim enough

	opts := []cgroups.Cgroup2ManagerOption{
		cgroups.WithCgroup2ProcessType(cgroups.ProcessTypePTY, "ptys", map[string]string{
			"cpu.weight":  "200", // gets much preferred cpu access, to help keep these real time
			"memory.high": fmt.Sprintf("%d", memoryHigh),
			"memory.max":  fmt.Sprintf("%d", memoryMax),
		}),
		cgroups.WithCgroup2ProcessType(cgroups.ProcessTypeSocat, "socats", map[string]string{
			"cpu.weight": "150", // gets slightly preferred cpu access
			"memory.min": fmt.Sprintf("%d", 5*megabyte),
			"memory.low": fmt.Sprintf("%d", 8*megabyte),
		}),
		cgroups.WithCgroup2ProcessType(cgroups.ProcessTypeUser, "user", map[string]string{
			"memory.high": fmt.Sprintf("%d", memoryHigh),
			"memory.max":  fmt.Sprintf("%d", memoryMax),
			"cpu.weight":  "50", // less than envd, and less than core processes that default to 100
		}),
	}
	if cgroupRoot != "" {
		opts = append(opts, cgroups.WithCgroup2RootSysFSPath(cgroupRoot))
	}

	mgr, err := cgroups.NewCgroup2Manager(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create cgroup2 manager: %v\n", err)

		return nil
	}

	return mgr
}
