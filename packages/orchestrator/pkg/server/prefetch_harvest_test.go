//go:build linux

package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// TestClampHarvestTimeoutMs locks the floor that protects the harvest from a
// misconfigured zero/negative timeout flag, which would otherwise yield an
// already-expired context that silently skips every harvest.
func TestClampHarvestTimeoutMs(t *testing.T) {
	t.Parallel()

	require.Equal(t, minHarvestTimeoutMs, clampHarvestTimeoutMs(0), "zero is floored")
	require.Equal(t, minHarvestTimeoutMs, clampHarvestTimeoutMs(-100), "negative is floored")
	require.Equal(t, minHarvestTimeoutMs, clampHarvestTimeoutMs(minHarvestTimeoutMs), "at the floor")
	require.Equal(t, minHarvestTimeoutMs+1, clampHarvestTimeoutMs(minHarvestTimeoutMs+1), "just above the floor is kept")
	require.Equal(t, 60000, clampHarvestTimeoutMs(60000), "the default is kept")
}

// --- fakes for the harvest orchestration ----------------------------------

type fakeHarvestInstance struct {
	data     block.PrefetchData
	dataErr  error
	stopErr  error
	closeErr error
	stopped  bool
	closed   bool
}

func (f *fakeHarvestInstance) MemoryPrefetchData(context.Context) (block.PrefetchData, error) {
	return f.data, f.dataErr
}

func (f *fakeHarvestInstance) Stop(context.Context) error {
	f.stopped = true

	return f.stopErr
}

func (f *fakeHarvestInstance) Close(context.Context) error {
	f.closed = true

	return f.closeErr
}

type fakeHarvestResumer struct {
	inst       harvestInstance
	err        error
	called     bool
	gotRuntime sandbox.RuntimeMetadata
	gotConfig  *sandbox.Config
}

func (f *fakeHarvestResumer) ResumeForHarvest(_ context.Context, _ sbxtemplate.Template, config *sandbox.Config, runtime sandbox.RuntimeMetadata, _, _ time.Time) (harvestInstance, error) {
	f.called = true
	f.gotRuntime = runtime
	f.gotConfig = config
	if f.err != nil {
		return nil, f.err
	}

	return f.inst, nil
}

type fakeHarvestTemplates struct {
	getErr      error
	updateErr   error
	updated     bool
	updatedMeta metadata.Template
}

func (f *fakeHarvestTemplates) GetTemplate(context.Context, string, bool, bool, ...sbxtemplate.GetTemplateOpts) (sbxtemplate.Template, error) {
	return nil, f.getErr
}

func (f *fakeHarvestTemplates) UpdateMetadata(_ string, meta metadata.Template) error {
	f.updated = true
	f.updatedMeta = meta

	return f.updateErr
}

type fakeHarvestUpload struct {
	err    error
	called bool
	onWait func()
}

func (f *fakeHarvestUpload) Wait(context.Context) error {
	f.called = true
	if f.onWait != nil {
		f.onWait()
	}

	return f.err
}

// harvestProbe wires a prefetchHarvester to fakes, defaulting to the happy path
// (resume succeeds, a 2-block trace, all collaborators succeed). Each test tweaks
// one field before calling run.
type harvestProbe struct {
	h        *prefetchHarvester
	resumer  *fakeHarvestResumer
	inst     *fakeHarvestInstance
	tmpls    *fakeHarvestTemplates
	upload   *fakeHarvestUpload
	released bool

	uploadCalled bool
	uploadErr    error
	acquireErr   error
}

func newHarvestProbe() *harvestProbe {
	p := &harvestProbe{
		inst: &fakeHarvestInstance{
			data: block.PrefetchData{
				BlockEntries: map[uint64]block.PrefetchBlockEntry{
					1: {Index: 1, Order: 1},
					2: {Index: 2, Order: 2},
				},
				BlockSize: 2 << 20,
			},
		},
		tmpls:  &fakeHarvestTemplates{},
		upload: &fakeHarvestUpload{},
	}
	p.resumer = &fakeHarvestResumer{inst: p.inst}
	p.h = &prefetchHarvester{
		resumer:   p.resumer,
		templates: p.tmpls,
		uploadMetadata: func(context.Context, metadata.Template, storage.ObjectMetadata) error {
			p.uploadCalled = true

			return p.uploadErr
		},
		acquire: func(context.Context) error { return p.acquireErr },
		release: func() { p.released = true },
	}

	return p
}

func (p *harvestProbe) run(ctx context.Context, consume bool) (int, harvestOutcome, error) {
	return p.h.run(ctx, testHarvestSandbox(), metadata.Template{}, p.upload, "build-1", storage.ObjectMetadata{}, consume)
}

func testHarvestSandbox() *sandbox.Sandbox {
	return &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			// A volume mount on the source sandbox, so TestHarvestRun_ThrowawayIdentity
			// can assert the throwaway resumes with it suppressed (DenyEgress blocks the
			// NFS mount /init would otherwise attempt).
			Config: sandbox.NewConfig(sandbox.Config{
				VolumeMounts: []sandbox.VolumeMountConfig{{Name: "vol-1"}},
			}),
			// BuildID deliberately differs from the harvest's buildID ("build-1")
			// so TestHarvestRun_ThrowawayIdentity catches a regression to it.
			Runtime: sandbox.RuntimeMetadata{SandboxID: "sandbox-1", TeamID: "team-1", BuildID: "original-build", TemplateID: "tmpl-1"},
		},
	}
}

// TestHarvestRun_ConsumePersistsAndReaps: with consume on and a non-empty trace,
// the harvest reaps the throwaway, updates local metadata, waits for the upload,
// and re-uploads the enriched metadata remotely.
func TestHarvestRun_ConsumePersistsAndReaps(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	pages, outcome, err := p.run(t.Context(), true)

	require.NoError(t, err)
	require.Equal(t, harvestSuccess, outcome)
	require.Equal(t, 2, pages)
	require.True(t, p.resumer.called)
	require.True(t, p.inst.stopped && p.inst.closed, "throwaway must be reaped")
	require.True(t, p.released, "start slot must be released")
	require.True(t, p.upload.called, "must wait for the in-flight upload before persisting")
	require.True(t, p.tmpls.updated, "local metadata must be updated")
	require.NotNil(t, p.tmpls.updatedMeta.Prefetch, "persisted metadata must carry the mapping")
	require.Equal(t, 2, p.tmpls.updatedMeta.Prefetch.Memory.Count(), "the persisted mapping must hold the harvested blocks")
	require.True(t, p.uploadCalled, "remote metadata must be re-uploaded")
}

// TestHarvestRun_ThrowawayIdentity checks the throwaway resume runtime: it is
// tagged with the resumed pause-snapshot build (buildID), not the original
// sandbox's build, and gets a distinct prefetch-harvest- SandboxID so it never
// collides with the original in the sandbox map.
func TestHarvestRun_ThrowawayIdentity(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	_, _, err := p.run(t.Context(), true)

	require.NoError(t, err)
	require.Equal(t, "build-1", p.resumer.gotRuntime.BuildID, "throwaway must use the resumed snapshot build, not the original sandbox's")
	require.Equal(t, "prefetch-harvest-sandbox-1", p.resumer.gotRuntime.SandboxID)
	require.Equal(t, "team-1", p.resumer.gotRuntime.TeamID)
	require.Equal(t, "tmpl-1", p.resumer.gotRuntime.TemplateID)
	require.NotNil(t, p.resumer.gotConfig)
	require.Nil(t, p.resumer.gotConfig.VolumeMounts,
		"throwaway must resume with volume mounts suppressed so /init does not attempt the (DenyEgress-blocked) NFS mount")
}

// TestHarvestRun_NoConsumePersistsNothing: with consume off, the harvest still
// resumes/reaps and reports the trace size, but writes no metadata (write-side
// gate — see design Decision 7).
func TestHarvestRun_NoConsumePersistsNothing(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	pages, outcome, err := p.run(t.Context(), false)

	require.NoError(t, err)
	require.Equal(t, harvestSuccess, outcome)
	require.Equal(t, 2, pages, "trace is still measured")
	require.True(t, p.inst.stopped && p.inst.closed, "throwaway must be reaped")
	require.True(t, p.released)
	require.False(t, p.upload.called, "no upload wait when not consuming")
	require.False(t, p.tmpls.updated, "no local metadata write when not consuming")
	require.False(t, p.uploadCalled, "no remote metadata write when not consuming")
}

// TestHarvestRun_EmptyTracePersistsNothing: an empty trace yields a nil mapping,
// so nothing is persisted even with consume on.
func TestHarvestRun_EmptyTracePersistsNothing(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	p.inst.data = block.PrefetchData{BlockEntries: map[uint64]block.PrefetchBlockEntry{}, BlockSize: 0}

	pages, outcome, err := p.run(t.Context(), true)

	require.NoError(t, err)
	require.Equal(t, harvestSuccess, outcome, "an empty trace is still a successful harvest")
	require.Zero(t, pages)
	require.True(t, p.inst.stopped && p.inst.closed, "throwaway must be reaped")
	require.False(t, p.tmpls.updated)
	require.False(t, p.uploadCalled)
}

// TestHarvestRun_ResumeErrorAbortsCleanly: a resume failure returns an error,
// reaps nothing (no instance), and still releases the slot.
func TestHarvestRun_ResumeErrorAbortsCleanly(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	p.resumer.err = errors.New("resume boom")

	_, outcome, err := p.run(t.Context(), true)

	require.Error(t, err)
	require.Equal(t, harvestResumeFailed, outcome)
	require.True(t, p.resumer.called)
	require.False(t, p.inst.stopped, "no instance to reap on resume failure")
	require.True(t, p.released, "slot acquired before the resume must still be released")
	require.False(t, p.tmpls.updated)
}

// TestHarvestRun_GetTemplateErrorAbortsCleanly: a template load failure returns
// before any resume; the slot is still released.
func TestHarvestRun_GetTemplateErrorAbortsCleanly(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	p.tmpls.getErr = errors.New("get template boom")

	_, outcome, err := p.run(t.Context(), true)

	require.Error(t, err)
	require.Equal(t, harvestResumeFailed, outcome)
	require.False(t, p.resumer.called, "must not resume if the template can't be loaded")
	require.True(t, p.released)
}

// TestHarvestRun_CollectErrorStillReaps: a failure collecting the trace returns
// an error but still reaps the throwaway (the §6 reap-on-every-path invariant).
func TestHarvestRun_CollectErrorStillReaps(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	p.inst.dataErr = errors.New("collect boom")

	_, outcome, err := p.run(t.Context(), true)

	require.Error(t, err)
	require.Equal(t, harvestCollectFailed, outcome)
	require.True(t, p.inst.stopped && p.inst.closed, "throwaway must be reaped even when collection fails")
	require.True(t, p.released)
	require.False(t, p.tmpls.updated)
}

// TestHarvestRun_AcquireErrorSkips: if the start slot can't be acquired the
// harvest is skipped — nothing is resumed and nothing is released (it never
// acquired).
func TestHarvestRun_AcquireErrorSkips(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	p.acquireErr = errors.New("at capacity")

	_, outcome, err := p.run(t.Context(), true)

	require.Error(t, err)
	require.Equal(t, harvestSkipped, outcome)
	require.False(t, p.resumer.called)
	require.False(t, p.released, "release must not run when acquire failed")
}

// TestHarvestRun_UploadFailedKeepsLocalSkipsRemote: if the snapshot upload
// failed, the local metadata is still updated (same-node resume can prefetch)
// but the remote re-upload is skipped (the remote build never landed).
func TestHarvestRun_UploadFailedKeepsLocalSkipsRemote(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	p.upload.err = errors.New("upload did not land")

	pages, outcome, err := p.run(t.Context(), true)

	require.NoError(t, err)
	require.Equal(t, harvestSuccess, outcome, "the trace was still harvested")
	require.Equal(t, 2, pages)
	require.True(t, p.upload.called)
	require.True(t, p.tmpls.updated, "local metadata is updated even when the upload failed")
	require.False(t, p.uploadCalled, "remote re-upload is skipped when the snapshot did not land")
}

// TestHarvestRun_DeadlineDuringWaitLeavesMetadataUntouched: if the harvest
// deadline fires while waiting for the upload, neither local nor remote metadata
// is touched (the upload may still be reading/writing them).
func TestHarvestRun_DeadlineDuringWaitLeavesMetadataUntouched(t *testing.T) {
	t.Parallel()

	p := newHarvestProbe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Simulate the harvest deadline firing during the upload wait.
	p.upload.onWait = cancel

	pages, outcome, err := p.run(ctx, true)

	require.NoError(t, err)
	require.Equal(t, harvestSuccess, outcome)
	require.Equal(t, 2, pages)
	require.True(t, p.upload.called)
	require.False(t, p.tmpls.updated, "must not rewrite the metafile while the upload may still be using it")
	require.False(t, p.uploadCalled)
}
