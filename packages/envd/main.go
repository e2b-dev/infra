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
	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	publicport "github.com/e2b-dev/infra/packages/envd/internal/port"
	filesystemRpc "github.com/e2b-dev/infra/packages/envd/internal/services/filesystem"
	processRpc "github.com/e2b-dev/infra/packages/envd/internal/services/process"
	processSpec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

const (
	// Downstream timeout should be greater than upstream (in orchestrator proxy).
	idleTimeout = 640 * time.Second
	maxAge      = 2 * time.Hour

	defaultPort = 49983

	portScannerInterval = 1000 * time.Millisecond
)

var (
	Version = "0.2.4"

	commitSHA string

	isNotFC bool
	port    int64

	versionFlag  bool
	commitFlag   bool
	startCmdFlag string
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

	flag.Parse()
}

func withCORS(h http.Handler) http.Handler {
	middleware := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{
			"GET",
			"POST",
		},
		AllowedHeaders: append(
			connectcors.AllowedHeaders(),
			"Origin",
			"Accept",
			"Authorization",
			"Content-Type",
			"Cache-Control",
			"X-Requested-With",
			"X-Content-Type-Options",
			"Access-Control-Request-Method",
			"Access-Control-Request-Headers",
			"Access-Control-Request-Private-Network",
			"Access-Control-Expose-Headers",
			"Keepalive-Ping-Interval", // for gRPC
			// Custom headers sent from SDK
			"browser",
			"lang",
			"lang_version",
			"machine",
			"os",
			"package_version",
			"processor",
			"publisher",
			"release",
			"sdk_runtime",
			"system",
		),
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

	envVars := utils.NewMap[string, string]()
	isFCBoolStr := strconv.FormatBool(!isNotFC)
	envVars.Store("E2B_SANDBOX", isFCBoolStr)
	if err := os.WriteFile(filepath.Join(host.E2BRunDir, ".E2B_SANDBOX"), []byte(isFCBoolStr), 0o444); err != nil {
		fmt.Fprintf(os.Stderr, "error writing sandbox file: %v\n", err)
	}

	mmdsChan := make(chan *host.MMDSOpts, 1)
	defer close(mmdsChan)
	if !isNotFC {
		go host.PollForMMDSOpts(ctx, mmdsChan, envVars)
	}

	l := logs.NewLogger(ctx, isNotFC, mmdsChan)

	m := chi.NewRouter()

	envLogger := l.With().Str("logger", "envd").Logger()
	fsLogger := l.With().Str("logger", "filesystem").Logger()
	filesystemRpc.Handle(m, &fsLogger)

	processLogger := l.With().Str("logger", "process").Logger()
	processService := processRpc.Handle(m, &processLogger, envVars)

	service := api.New(&envLogger, envVars, mmdsChan, isNotFC)
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
		if err == nil {
			processService.InitializeStartProcess(ctx, user, &processSpec.StartRequest{
				Tag: &tag,
				Process: &processSpec.ProcessConfig{
					Envs: make(map[string]string),
					Cmd:  "/bin/bash",
					Args: []string{"-l", "-c", startCmdFlag},
					Cwd:  &cwd,
				},
			})
		} else {
			log.Fatalf("error getting user: %v", err)
		}
	}

	// Bind all open ports on 127.0.0.1 and localhost to the eth0 interface
	portScanner := publicport.NewScanner(portScannerInterval)
	defer portScanner.Destroy()

	portLogger := l.With().Str("logger", "port-forwarder").Logger()
	portForwarder := publicport.NewForwarder(&portLogger, portScanner)
	go portForwarder.StartForwarding()

	go portScanner.ScanAndBroadcast()

	err := s.ListenAndServe()
	if err != nil {
		log.Fatalf("error starting server: %v", err)
	}
}
