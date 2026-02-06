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
	processSpec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

const (
	// Downstream timeout should be greater than upstream (in orchestrator proxy).
	idleTimeout  = 640 * time.Second
	readTimeout  = 30 * time.Second
	writeTimeout = 5 * time.Minute
	maxAge       = 2 * time.Hour

	defaultPort = 49983

	portScannerInterval = 1000 * time.Millisecond

	// This is the default user used in the container if not specified otherwise.
	// It should be always overridden by the user in /init when building the template.
	defaultUser = "root"

	kilobyte = 1024
	megabyte = 1024 * kilobyte
)

var (
	Version = "0.5.1"

	commitSHA string

	isNotFC bool
	port    int64

	versionFlag  bool
	commitFlag   bool
	startCmdFlag string
	cgroupRoot   string
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
		fmt.Printf("%s\n", Version)

		return
	}

	if commitFlag {
		fmt.Printf("%s\n", commitSHA)

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

	service := api.New(&envLogger, defaults, mmdsChan, isNotFC)
	handler := api.HandlerFromMux(service, m)
	middleware := authn.NewMiddleware(permissions.AuthenticateUsername)

	s := &http.Server{
		Handler: withCORS(
			service.WithAuthorization(
				middleware.Wrap(handler),
			),
		),
		Addr:         fmt.Sprintf("0.0.0.0:%d", port),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
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
	maxMemoryReserved := uint64(float64(metrics.MemTotal) * .125)
	maxMemoryReserved = min(maxMemoryReserved, uint64(128)*megabyte)

	opts := []cgroups.Cgroup2ManagerOption{
		cgroups.WithCgroup2ProcessType(cgroups.ProcessTypePTY, "ptys", map[string]string{
			"cpu.weight": "200", // gets much preferred cpu access, to help keep these real time
		}),
		cgroups.WithCgroup2ProcessType(cgroups.ProcessTypeSocat, "socats", map[string]string{
			"cpu.weight": "150", // gets slightly preferred cpu access
			"memory.min": fmt.Sprintf("%d", 5*megabyte),
			"memory.low": fmt.Sprintf("%d", 8*megabyte),
		}),
		cgroups.WithCgroup2ProcessType(cgroups.ProcessTypeUser, "user", map[string]string{
			"memory.high": fmt.Sprintf("%d", metrics.MemTotal-maxMemoryReserved),
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
