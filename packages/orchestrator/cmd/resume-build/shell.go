package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/term"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process/processconnect"
)

// shellExitedError is returned when an interactive shell session ends.
// Callers in pause modes use it to distinguish "user detached" from real errors.
type shellExitedError struct{ exitCode int32 }

func (e *shellExitedError) Error() string {
	return fmt.Sprintf("shell exited with code %d", e.exitCode)
}

func isShellExited(err error) bool {
	var s *shellExitedError

	return errors.As(err, &s)
}

// shellEnv builds the environment passed into the in-guest PTY shell.
// envd intentionally only inherits PATH/HOME/USER/LOGNAME plus its own
// configured globals, so we must propagate TERM (and a few common locale
// vars) explicitly — otherwise curses apps like htop, tmux, vim and less
// fail to initialise.
func shellEnv() map[string]string {
	envs := map[string]string{}

	if t := os.Getenv("TERM"); t != "" {
		envs["TERM"] = t
	} else {
		envs["TERM"] = "xterm-256color"
	}

	for _, k := range []string{"LANG", "LC_ALL", "LC_CTYPE", "COLORTERM"} {
		if v := os.Getenv(k); v != "" {
			envs[k] = v
		}
	}

	return envs
}

// attachShell opens an interactive PTY shell against envd inside sbx,
// proxying the host terminal through. It tries /bin/bash -l first and
// falls back to /bin/sh if bash is missing in the guest.
//
// Returns when the in-guest shell exits (Ctrl+D), or when ctx is cancelled.
func attachShell(ctx context.Context, sbx *sandbox.Sandbox) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("-shell requires an interactive terminal on stdin")
	}

	envdURL := fmt.Sprintf("http://%s:%d", sbx.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	hc := http.Client{
		// No request timeout — interactive sessions can be long-lived.
		Transport: sandbox.SandboxHttpTransport,
	}
	processC := processconnect.NewProcessClient(&hc, envdURL)

	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || cols == 0 || rows == 0 {
		cols, rows = 80, 24
	}

	for _, candidate := range []struct {
		cmd  string
		args []string
	}{
		{"/bin/bash", []string{"-l"}},
		{"/bin/sh", []string{"-l"}},
	} {
		err := runShell(ctx, processC, sbx, candidate.cmd, candidate.args, uint32(cols), uint32(rows))
		// Fall back to the next candidate if the shell binary is missing or
		// failed before we ever saw any output.
		if isMissingShell(err) {
			continue
		}

		return err
	}

	return errors.New("no usable shell found in sandbox (tried /bin/bash, /bin/sh)")
}

// missingShellError signals that a candidate shell binary failed to launch
// and we should try the next one.
type missingShellError struct{ inner error }

func (e *missingShellError) Error() string { return e.inner.Error() }
func (e *missingShellError) Unwrap() error { return e.inner }

func isMissingShell(err error) bool {
	var m *missingShellError

	return errors.As(err, &m)
}

func runShell(
	ctx context.Context,
	processC processconnect.ProcessClient,
	sbx *sandbox.Sandbox,
	cmd string,
	args []string,
	cols, rows uint32,
) error {
	startReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  cmd,
			Args: args,
			Envs: shellEnv(),
		},
		Pty: &process.PTY{
			Size: &process.PTY_Size{Cols: cols, Rows: rows},
		},
	})
	grpc.SetUserHeader(startReq.Header(), "root")
	if sbx.Config.Envd.AccessToken != nil {
		startReq.Header().Set("X-Access-Token", *sbx.Config.Envd.AccessToken)
	}

	stream, err := processC.Start(ctx, startReq)
	if err != nil {
		return &missingShellError{inner: fmt.Errorf("start %s: %w", cmd, err)}
	}
	defer stream.Close()

	// Wait for the StartEvent so we have a pid to address input/resize at.
	var pid uint32
	gotData := false
	for stream.Receive() {
		event := stream.Msg().GetEvent().GetEvent()
		switch e := event.(type) {
		case *process.ProcessEvent_Start:
			pid = e.Start.GetPid()
		case *process.ProcessEvent_Data:
			gotData = true
			// Push any data that arrived before we exited the bootstrap loop.
			if pty := e.Data.GetPty(); pty != nil {
				_, _ = os.Stdout.Write(pty)
			}
		case *process.ProcessEvent_End:
			// Process ended before producing output — treat as missing-shell
			// so the caller can try the fallback.
			if !gotData {
				return &missingShellError{inner: fmt.Errorf("%s exited immediately (code %d)", cmd, e.End.GetExitCode())}
			}

			return endToError(e.End)
		}
		if pid != 0 {
			break
		}
	}
	if pid == 0 {
		if err := stream.Err(); err != nil {
			return &missingShellError{inner: fmt.Errorf("stream closed before start: %w", err)}
		}

		return &missingShellError{inner: errors.New("no start event received")}
	}

	fmt.Println("📟 Attaching shell via envd (Ctrl+D to exit)")

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer func() {
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
		fmt.Println()
	}()

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Output pump: stream → stdout. Closes sessionCtx on exit so input/resize
	// goroutines unwind too.
	endCh := make(chan *process.ProcessEvent_EndEvent, 1)
	go func() {
		defer cancel()
		for stream.Receive() {
			event := stream.Msg().GetEvent().GetEvent()
			switch e := event.(type) {
			case *process.ProcessEvent_Data:
				if pty := e.Data.GetPty(); pty != nil {
					_, _ = os.Stdout.Write(pty)
				}
			case *process.ProcessEvent_End:
				endCh <- e.End

				return
			}
		}
	}()

	// Input pump: stdin → StreamInput as PTY bytes.
	go pumpInput(sessionCtx, processC, sbx, pid)

	// Resize: forward SIGWINCH to envd via Update.
	go pumpResize(sessionCtx, processC, sbx, pid)

	<-sessionCtx.Done()

	select {
	case end := <-endCh:
		return endToError(end)
	default:
	}

	if err := stream.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("shell stream: %w", err)
	}

	return nil
}

func pumpInput(
	ctx context.Context,
	processC processconnect.ProcessClient,
	sbx *sandbox.Sandbox,
	pid uint32,
) {
	in := processC.StreamInput(ctx)
	grpc.SetUserHeader(in.RequestHeader(), "root")
	if sbx.Config.Envd.AccessToken != nil {
		in.RequestHeader().Set("X-Access-Token", *sbx.Config.Envd.AccessToken)
	}

	if err := in.Send(&process.StreamInputRequest{
		Event: &process.StreamInputRequest_Start{
			Start: &process.StreamInputRequest_StartEvent{
				Process: &process.ProcessSelector{
					Selector: &process.ProcessSelector_Pid{Pid: pid},
				},
			},
		},
	}); err != nil {
		return
	}

	buf := make([]byte, 4096)
	for {
		// In raw mode, Read blocks until a byte arrives. We can't easily
		// interrupt it on ctx.Done, but the parent process will exit soon
		// after the stream closes, which is acceptable for a CLI.
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			if sendErr := in.Send(&process.StreamInputRequest{
				Event: &process.StreamInputRequest_Data{
					Data: &process.StreamInputRequest_DataEvent{
						Input: &process.ProcessInput{
							Input: &process.ProcessInput_Pty{Pty: data},
						},
					},
				},
			}); sendErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func pumpResize(
	ctx context.Context,
	processC processconnect.ProcessClient,
	sbx *sandbox.Sandbox,
	pid uint32,
) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	for {
		select {
		case <-ctx.Done():
			return
		case <-winch:
			cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil || cols == 0 || rows == 0 {
				continue
			}
			req := connect.NewRequest(&process.UpdateRequest{
				Process: &process.ProcessSelector{
					Selector: &process.ProcessSelector_Pid{Pid: pid},
				},
				Pty: &process.PTY{
					Size: &process.PTY_Size{Cols: uint32(cols), Rows: uint32(rows)},
				},
			})
			grpc.SetUserHeader(req.Header(), "root")
			if sbx.Config.Envd.AccessToken != nil {
				req.Header().Set("X-Access-Token", *sbx.Config.Envd.AccessToken)
			}
			updateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, _ = processC.Update(updateCtx, req)
			cancel()
		}
	}
}

func endToError(end *process.ProcessEvent_EndEvent) error {
	if end == nil {
		return nil
	}

	return &shellExitedError{exitCode: end.GetExitCode()}
}
