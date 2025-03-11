package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/api"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	filesystemRpc "github.com/e2b-dev/infra/packages/envd/internal/services/filesystem"
	processRpc "github.com/e2b-dev/infra/packages/envd/internal/services/process"
	processSpec "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"

	"connectrpc.com/authn"
	connectcors "connectrpc.com/cors"
	"github.com/go-chi/chi/v5"
	"github.com/rs/cors"
)

const (
	// We limit the timeout more in proxies.
	maxTimeout = 24 * time.Hour
	maxAge     = 2 * time.Hour

	defaultPort = 49983
)

var (
	// These vars are automatically set by goreleaser.
	Version = "0.1.5"

	commitSHA string

	debug bool
	port  int64

	versionFlag  bool
	commitFlag   bool
	startCmdFlag string
)

func parseFlags() {
	flag.BoolVar(
		&debug,
		"debug",
		false,
		"debug mode prints all logs to stdout",
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

	l := logs.NewLogger(ctx, debug)

	m := chi.NewRouter()

	envLogger := l.With().Str("logger", "envd").Logger()
	fsLogger := l.With().Str("logger", "filesystem").Logger()
	filesystemRpc.Handle(m, &fsLogger)

	envVars := utils.NewMap[string, string]()

	envVars.Store("E2B_SANDBOX", "true")

	processLogger := l.With().Str("logger", "process").Logger()
	processService := processRpc.Handle(m, &processLogger, envVars)

	handler := api.HandlerFromMux(api.New(&envLogger, envVars), m)

	middleware := authn.NewMiddleware(permissions.AuthenticateUsername)

	s := &http.Server{
		Handler:           withCORS(middleware.Wrap(handler)),
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       maxTimeout,
		WriteTimeout:      maxTimeout,
		IdleTimeout:       maxTimeout,
	}

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

	err := s.ListenAndServe()
	if err != nil {
		log.Fatalf("error starting server: %v", err)
	}
}
