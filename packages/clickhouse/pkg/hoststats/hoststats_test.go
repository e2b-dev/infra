package hoststats

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDelivery struct {
	pushed   []SandboxHostStat
	pushErr  error
	closed   bool
	closeErr error
}

func (f *fakeDelivery) Push(stat SandboxHostStat) error {
	f.pushed = append(f.pushed, stat)

	return f.pushErr
}

func (f *fakeDelivery) Close(_ context.Context) error {
	f.closed = true

	return f.closeErr
}

func TestMultiDelivery_PushFansOutToAllTargets(t *testing.T) {
	t.Parallel()

	a, b := &fakeDelivery{}, &fakeDelivery{}
	md := NewMultiDelivery(a, b)

	stat := SandboxHostStat{SandboxID: "sbx-1"}
	require.NoError(t, md.Push(stat))

	assert.Equal(t, []SandboxHostStat{stat}, a.pushed)
	assert.Equal(t, []SandboxHostStat{stat}, b.pushed)
}

func TestMultiDelivery_PushContinuesAfterTargetError(t *testing.T) {
	t.Parallel()

	bad := &fakeDelivery{pushErr: errors.New("boom")}
	good := &fakeDelivery{}
	md := NewMultiDelivery(bad, good)

	stat := SandboxHostStat{SandboxID: "sbx-1"}
	err := md.Push(stat)

	require.Error(t, err)
	require.ErrorContains(t, err, "boom")
	// Good target still received the stat — best-effort, no early return.
	assert.Equal(t, []SandboxHostStat{stat}, good.pushed)
	assert.Equal(t, []SandboxHostStat{stat}, bad.pushed)
}

func TestMultiDelivery_CloseAllTargets(t *testing.T) {
	t.Parallel()

	a := &fakeDelivery{closeErr: errors.New("a closed badly")}
	b := &fakeDelivery{}
	md := NewMultiDelivery(a, b)

	err := md.Close(context.Background())

	require.Error(t, err)
	require.ErrorContains(t, err, "a closed badly")
	assert.True(t, a.closed)
	assert.True(t, b.closed)
}

func TestMultiDelivery_PushJoinsAllErrors(t *testing.T) {
	t.Parallel()

	errA := errors.New("push failed A")
	errB := errors.New("push failed B")
	a := &fakeDelivery{pushErr: errA}
	b := &fakeDelivery{pushErr: errB}
	md := NewMultiDelivery(a, b)

	stat := SandboxHostStat{SandboxID: "sbx-1"}
	err := md.Push(stat)

	require.Error(t, err)
	require.ErrorIs(t, err, errA)
	require.ErrorIs(t, err, errB)
	assert.Equal(t, []SandboxHostStat{stat}, a.pushed)
	assert.Equal(t, []SandboxHostStat{stat}, b.pushed)
}

func TestMultiDelivery_CloseJoinsAllErrors(t *testing.T) {
	t.Parallel()

	errA := errors.New("close failed A")
	errB := errors.New("close failed B")
	a := &fakeDelivery{closeErr: errA}
	b := &fakeDelivery{closeErr: errB}
	md := NewMultiDelivery(a, b)

	err := md.Close(context.Background())

	require.Error(t, err)
	require.ErrorIs(t, err, errA)
	require.ErrorIs(t, err, errB)
	assert.True(t, a.closed)
	assert.True(t, b.closed)
}

func TestMultiDelivery_EmptyTargets(t *testing.T) {
	t.Parallel()

	md := NewMultiDelivery()
	assert.NoError(t, md.Push(SandboxHostStat{}))
	assert.NoError(t, md.Close(context.Background()))
}

func TestMultiDelivery_SingleTargetBypassesWrapper(t *testing.T) {
	t.Parallel()

	single := &fakeDelivery{}
	md := NewMultiDelivery(single)

	// One-target case returns the underlying delivery directly — no wrapper.
	assert.Same(t, single, md)

	stat := SandboxHostStat{SandboxID: "sbx-1"}
	require.NoError(t, md.Push(stat))
	assert.Equal(t, []SandboxHostStat{stat}, single.pushed)

	require.NoError(t, md.Close(context.Background()))
	assert.True(t, single.closed)
}
