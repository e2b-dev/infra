package userprofile

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

type fakeProvider struct {
	byID    map[uuid.UUID]Profile
	byEmail map[string][]Profile
	context *sharedteamprovision.CreatorContextV1
	err     error
	calls   int
}

func (f *fakeProvider) GetProfilesByUserID(_ context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}

	result := make(map[uuid.UUID]Profile)
	for _, id := range userIDs {
		if profile, ok := f.byID[id]; ok {
			result[id] = profile
		}
	}

	return result, nil
}

func (f *fakeProvider) FindProfilesByEmail(_ context.Context, email string) ([]Profile, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}

	return f.byEmail[email], nil
}

func (f *fakeProvider) GetTeamCreatorContext(_ context.Context, _ uuid.UUID) (*sharedteamprovision.CreatorContextV1, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}

	return f.context, nil
}

func TestDualProvider_GetProfilesByUserID_PrefersSecondaryFallsBackToPrimary(t *testing.T) {
	t.Parallel()

	primaryOnly := uuid.New()
	bothShared := uuid.New()
	secondaryOnly := uuid.New()
	missing := uuid.New()

	primary := &fakeProvider{
		byID: map[uuid.UUID]Profile{
			primaryOnly: {UserID: primaryOnly, Email: "primary-only@example.com"},
			bothShared:  {UserID: bothShared, Email: "primary-loses@example.com"},
		},
	}
	secondary := &fakeProvider{
		byID: map[uuid.UUID]Profile{
			bothShared:    {UserID: bothShared, Email: "secondary-wins@example.com"},
			secondaryOnly: {UserID: secondaryOnly, Email: "secondary-only@example.com"},
		},
	}

	dual := newDualProvider(primary, secondary)
	got, err := dual.GetProfilesByUserID(t.Context(), []uuid.UUID{primaryOnly, bothShared, secondaryOnly, missing})
	if err != nil {
		t.Fatalf("GetProfilesByUserID() error = %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d profiles, want 3: %+v", len(got), got)
	}
	if got[primaryOnly].Email != "primary-only@example.com" {
		t.Fatalf("primaryOnly email = %q, want %q", got[primaryOnly].Email, "primary-only@example.com")
	}
	if got[bothShared].Email != "secondary-wins@example.com" {
		t.Fatalf("bothShared email = %q, want secondary to win", got[bothShared].Email)
	}
	if got[secondaryOnly].Email != "secondary-only@example.com" {
		t.Fatalf("secondaryOnly email = %q, want %q", got[secondaryOnly].Email, "secondary-only@example.com")
	}
	if _, ok := got[missing]; ok {
		t.Fatalf("expected missing user to be absent, got %+v", got[missing])
	}
}

func TestDualProvider_GetProfilesByUserID_ChecksSecondaryWhenPrimaryFullyResolves(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	primary := &fakeProvider{byID: map[uuid.UUID]Profile{id: {UserID: id, Email: "p@example.com"}}}
	secondary := &fakeProvider{}

	dual := newDualProvider(primary, secondary)
	if _, err := dual.GetProfilesByUserID(t.Context(), []uuid.UUID{id}); err != nil {
		t.Fatalf("GetProfilesByUserID() error = %v", err)
	}
	if secondary.calls != 1 {
		t.Fatalf("secondary calls = %d, want 1", secondary.calls)
	}
}

func TestDualProvider_GetProfilesByUserID_PropagatesPrimaryError(t *testing.T) {
	t.Parallel()

	primary := &fakeProvider{err: errors.New("primary boom")}
	secondary := &fakeProvider{}

	dual := newDualProvider(primary, secondary)
	_, err := dual.GetProfilesByUserID(t.Context(), []uuid.UUID{uuid.New()})
	if err == nil || err.Error() != "primary boom" {
		t.Fatalf("expected primary error, got %v", err)
	}
}

func TestDualProvider_GetProfilesByUserID_PropagatesSecondaryError(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	primary := &fakeProvider{byID: map[uuid.UUID]Profile{id: {UserID: id, Email: "p@example.com"}}}
	secondary := &fakeProvider{err: errors.New("secondary boom")}

	dual := newDualProvider(primary, secondary)
	_, err := dual.GetProfilesByUserID(t.Context(), []uuid.UUID{id})
	if err == nil || err.Error() != "secondary boom" {
		t.Fatalf("expected secondary error, got %v", err)
	}
}

func TestDualProvider_FindProfilesByEmail_PrefersSecondary(t *testing.T) {
	t.Parallel()

	sharedID := uuid.New()

	primary := &fakeProvider{byEmail: map[string][]Profile{
		"shared@example.com": {{UserID: sharedID, Email: "shared@example.com", Name: "primary-name"}},
	}}
	secondary := &fakeProvider{byEmail: map[string][]Profile{
		"shared@example.com": {
			{UserID: sharedID, Email: "shared@example.com", Name: "secondary-name"},
			{UserID: sharedID, Email: "shared@example.com", Name: "secondary-duplicate"},
		},
	}}

	dual := newDualProvider(primary, secondary)
	got, err := dual.FindProfilesByEmail(t.Context(), "shared@example.com")
	if err != nil {
		t.Fatalf("FindProfilesByEmail() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("got %d profiles, want 1: %+v", len(got), got)
	}
	if got[0].Name != "secondary-name" {
		t.Fatalf("name = %q, want secondary-name", got[0].Name)
	}
}

func TestDualProvider_FindProfilesByEmail_FallsBackToPrimary(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	primary := &fakeProvider{byEmail: map[string][]Profile{
		"primary@example.com": {{UserID: id, Email: "primary@example.com", Name: "primary-name"}},
	}}
	secondary := &fakeProvider{}

	dual := newDualProvider(primary, secondary)
	got, err := dual.FindProfilesByEmail(t.Context(), "primary@example.com")
	if err != nil {
		t.Fatalf("FindProfilesByEmail() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("got %d profiles, want 1: %+v", len(got), got)
	}
	if got[0].Name != "primary-name" {
		t.Fatalf("name = %q, want primary-name", got[0].Name)
	}
}

func TestDualProvider_GetTeamCreatorContext_PrefersSecondary(t *testing.T) {
	t.Parallel()

	primary := &fakeProvider{context: &sharedteamprovision.CreatorContextV1{
		IPAddress:  "203.0.113.10",
		UserAgent:  "Primary/1.0",
		AuthMethod: sharedteamprovision.AuthMethodPassword,
	}}
	secondary := &fakeProvider{context: &sharedteamprovision.CreatorContextV1{
		AuthMethod: sharedteamprovision.AuthMethodSocial,
	}}

	dual := newDualProvider(primary, secondary)
	got, err := dual.GetTeamCreatorContext(t.Context(), uuid.New())
	if err != nil {
		t.Fatalf("GetTeamCreatorContext() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetTeamCreatorContext() returned nil")
	}
	if got.IPAddress != "" || got.UserAgent != "" || got.AuthMethod != sharedteamprovision.AuthMethodSocial {
		t.Fatalf("GetTeamCreatorContext() = %+v, want secondary context", got)
	}
}

func TestDualProvider_GetTeamCreatorContext_FallsBackToPrimary(t *testing.T) {
	t.Parallel()

	primaryContext := &sharedteamprovision.CreatorContextV1{
		IPAddress:  "203.0.113.10",
		UserAgent:  "Primary/1.0",
		AuthMethod: sharedteamprovision.AuthMethodPassword,
	}
	primary := &fakeProvider{context: primaryContext}
	secondary := &fakeProvider{}

	dual := newDualProvider(primary, secondary)
	got, err := dual.GetTeamCreatorContext(t.Context(), uuid.New())
	if err != nil {
		t.Fatalf("GetTeamCreatorContext() error = %v", err)
	}
	if got != primaryContext {
		t.Fatalf("GetTeamCreatorContext() = %+v, want primary context", got)
	}
}

func TestDualProvider_GetTeamCreatorContext_PropagatesPrimaryError(t *testing.T) {
	t.Parallel()

	primary := &fakeProvider{err: errors.New("primary boom")}
	secondary := &fakeProvider{context: &sharedteamprovision.CreatorContextV1{IPAddress: "198.51.100.20"}}

	dual := newDualProvider(primary, secondary)
	_, err := dual.GetTeamCreatorContext(t.Context(), uuid.New())
	if err == nil || err.Error() != "primary boom" {
		t.Fatalf("expected primary error, got %v", err)
	}
	if secondary.calls != 0 {
		t.Fatalf("secondary calls = %d, want 0", secondary.calls)
	}
}

func TestDualProvider_GetTeamCreatorContext_PropagatesSecondaryError(t *testing.T) {
	t.Parallel()

	primary := &fakeProvider{context: &sharedteamprovision.CreatorContextV1{IPAddress: "203.0.113.10"}}
	secondary := &fakeProvider{err: errors.New("secondary boom")}

	dual := newDualProvider(primary, secondary)
	_, err := dual.GetTeamCreatorContext(t.Context(), uuid.New())
	if err == nil || err.Error() != "secondary boom" {
		t.Fatalf("expected secondary error, got %v", err)
	}
}

func TestNewProvider_FactorySelection(t *testing.T) {
	t.Parallel()

	supa := &fakeProvider{}
	ory := &fakeProvider{}

	tests := []struct {
		name     string
		mode     Mode
		supa     Provider
		ory      Provider
		wantErr  bool
		wantDual bool
	}{
		{name: "supabase mode returns supabase", mode: ModeSupabase, supa: supa, ory: ory},
		{name: "ory mode returns ory", mode: ModeOry, supa: supa, ory: ory},
		{name: "fallback mode returns dual", mode: ModeSupabaseOryFallback, supa: supa, ory: ory, wantDual: true},
		{name: "supabase mode requires supa", mode: ModeSupabase, supa: nil, ory: ory, wantErr: true},
		{name: "ory mode requires ory", mode: ModeOry, supa: supa, ory: nil, wantErr: true},
		{name: "fallback requires both", mode: ModeSupabaseOryFallback, supa: supa, ory: nil, wantErr: true},
		{name: "unknown mode errors", mode: Mode("nope"), supa: supa, ory: ory, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewProvider(tt.mode, tt.supa, tt.ory)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}
			if err != nil {
				t.Fatalf("NewProvider() error = %v", err)
			}
			_, isDual := got.(*dualProvider)
			if isDual != tt.wantDual {
				t.Fatalf("got dual = %v, want %v", isDual, tt.wantDual)
			}
		})
	}
}
