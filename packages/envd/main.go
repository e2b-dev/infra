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

	"github.com/e2b-dev/infra/packages/envd/internal/api"
	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	publicport "github.com/e2b-dev/infra/packages/envd/internal/port"
	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	filesystemRpc "github.com/e2b-dev/infra/packages/envd/internal/services/filesystem"
	processRpc "github.com/e2b-dev/infra/packages/envd/internal/services/process"
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

	versionFlag bool
	commitFlag  bool
	cgroupRoot  string
	noCgroups   bool
	verbose     bool
)

func parseFlags() {
	flag.BoolVar(
		&isNotFC,
		"isnotfc",
		false,
		"run outside of Firecracker (skips MMDS poll and HTTP log exporter)",
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
		&cgroupRoot,
		"cgroup-root",
		"/sys/fs/cgroup",
		"cgroup root directory",
	)

	flag.BoolVar(
		&noCgroups,
		"no-cgroups",
		false,
		"disable cgroup management; use a no-op cgroup manager instead",
	)

	flag.BoolVar(
		&verbose,
		"verbose",
		false,
		"write envd logs to stdout",
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

	if err := run(); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := os.MkdirAll(host.E2BRunDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating E2B run directory: %v\n", err)
	}

	defaults := &execcontext.Defaults{
		User:    defaultUser,
		EnvVars: utils.NewEnvVars(),
	}
	isFCBoolStr := strconv.FormatBool(!isNotFC)
	defaults.EnvVars.Store("E2B_SANDBOX", isFCBoolStr)
	if err := os.WriteFile(filepath.Join(host.E2BRunDir, ".E2B_SANDBOX"), []byte(isFCBoolStr), 0o444); err != nil {
		fmt.Fprintf(os.Stderr, "error writing sandbox file: %v\n", err)
	}

	// Not closed - producers may outlive the consumer.
	mmdsChan := make(chan *host.MMDSOpts, 1)
	if !isNotFC {
		go host.PollForMMDSOpts(ctx, mmdsChan, defaults.EnvVars)
	}

	l := logs.NewLogger(ctx, !isNotFC, verbose, mmdsChan)

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
	processRpc.Handle(m, &processLogger, defaults, cgroupManager)

	service := api.New(&envLogger, defaults, mmdsChan, isNotFC, cgroupManager)
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

	// Bind all open ports on 127.0.0.1 and localhost to the eth0 interface
	portScanner := publicport.NewScanner(portScannerInterval)
	defer portScanner.Destroy()

	portLogger := l.With().Str("logger", "port-forwarder").Logger()
	portForwarder := publicport.NewForwarder(&portLogger, portScanner, cgroupManager)
	go portForwarder.StartForwarding(ctx)

	go portScanner.ScanAndBroadcast()

	err := s.ListenAndServe()
	// Signal goroutines to stop before deferred cleanup closes their resources.
	// TODO: shutdown synchronization needs to be revisited.
	cancel()

	return err
}

func createCgroupManager() (m cgroups.Manager) {
	defer func() {
		if m == nil {
			fmt.Fprintf(os.Stderr, "falling back to no-op cgroup manager\n")
			m = cgroups.NewNoopManager()
		}
	}()

	// Explicit opt-out: callers can tell envd to skip cgroup setup entirely.
	// We return the no-op manager directly without touching /sys/fs/cgroup
	// so we don't log spurious errors.
	if noCgroups {
		fmt.Fprintf(os.Stderr, "cgroups disabled via --no-cgroups; using no-op cgroup manager\n")

		return cgroups.NewNoopManager()
	}

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
			"cpu.weight":  "200",
			"io.weight":   "default 50",
			"memory.high": fmt.Sprintf("%d", memoryHigh),
			"memory.max":  fmt.Sprintf("%d", memoryMax),
		}),
		cgroups.WithCgroup2ProcessType(cgroups.ProcessTypeSocat, "socats", map[string]string{
			"cpu.weight": "150",
			"io.weight":  "default 50",
			"memory.min": fmt.Sprintf("%d", 5*megabyte),
			"memory.low": fmt.Sprintf("%d", 8*megabyte),
		}),
		cgroups.WithCgroup2ProcessType(cgroups.ProcessTypeUser, "user", map[string]string{
			"memory.high": fmt.Sprintf("%d", memoryHigh),
			"memory.max":  fmt.Sprintf("%d", memoryMax),
			"cpu.weight":  "50",
			"io.weight":   "default 10",
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
