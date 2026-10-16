package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
)

// fakeProcessStream replays events, then reports err as the stream's end.
type fakeProcessStream struct {
	events []*process.ProcessEvent
	err    error
	cur    *process.ProcessEvent
}

func (f *fakeProcessStream) Receive() bool {
	if len(f.events) == 0 {
		return false
	}
	f.cur, f.events = f.events[0], f.events[1:]

	return true
}

func (f *fakeProcessStream) Msg() *process.StartResponse { return &process.StartResponse{Event: f.cur} }
func (f *fakeProcessStream) Err() error                  { return f.err }

func stdoutEvent(s string) *process.ProcessEvent {
	return &process.ProcessEvent{Event: &process.ProcessEvent_Data{Data: &process.ProcessEvent_DataEvent{
		Output: &process.ProcessEvent_DataEvent_Stdout{Stdout: []byte(s)},
	}}}
}

func endEvent(code int32) *process.ProcessEvent {
	return &process.ProcessEvent{Event: &process.ProcessEvent_End{End: &process.ProcessEvent_EndEvent{Exited: true, ExitCode: code}}}
}

// A capture is complete only when the End event arrives: a stream that closes
// early is an error, so two equally truncated outputs can never compare equal.
func TestCaptureProcessOutput(t *testing.T) {
	t.Parallel()

	t.Run("complete", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		err := captureProcessOutput(&fakeProcessStream{events: []*process.ProcessEvent{stdoutEvent("HASH a 1\n"), stdoutEvent("HASH b 2\n"), endEvent(0)}}, &out)
		require.NoError(t, err)
		require.Equal(t, "HASH a 1\nHASH b 2\n", out.String())
	})

	t.Run("closed before the end event", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		err := captureProcessOutput(&fakeProcessStream{events: []*process.ProcessEvent{stdoutEvent("HASH a 1\n")}}, &out)
		require.ErrorContains(t, err, "without an exit status")
		require.Equal(t, "HASH a 1\n", out.String(), "the partial output is still delivered for diagnosis")
	})

	t.Run("stream error", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		err := captureProcessOutput(&fakeProcessStream{events: []*process.ProcessEvent{stdoutEvent("x")}, err: errors.New("connection reset")}, &out)
		require.ErrorContains(t, err, "connection reset")
	})

	t.Run("non-zero exit", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		err := captureProcessOutput(&fakeProcessStream{events: []*process.ProcessEvent{endEvent(3)}}, &out)
		require.ErrorContains(t, err, "exited with code 3")
	})
}
