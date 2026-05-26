package userprofile

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type fakeProvider struct {
	byID    map[uuid.UUID]Profile
	byEmail map[string][]Profile
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

func TestDualProvider_GetProfilesByUserID_PrefersPrimaryFallsBackToSecondary(t *testing.T) {
	t.Parallel()

	primaryOnly := uuid.New()
	bothShared := uuid.New()
	secondaryOnly := uuid.New()
	missing := uuid.New()

	primary := &fakeProvider{
		byID: map[uuid.UUID]Profile{
			primaryOnly: {UserID: primaryOnly, Email: "primary-only@example.com"},
			bothShared:  {UserID: bothShared, Email: "primary-wins@example.com"},
		},
	}
	secondary := &fakeProvider{
		byID: map[uuid.UUID]Profile{
			bothShared:    {UserID: bothShared, Email: "secondary-loses@example.com"},
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
	if got[bothShared].Email != "primary-wins@example.com" {
		t.Fatalf("bothShared email = %q, want primary to win", got[bothShared].Email)
	}
	if got[secondaryOnly].Email != "secondary-only@example.com" {
		t.Fatalf("secondaryOnly email = %q, want %q", got[secondaryOnly].Email, "secondary-only@example.com")
	}
	if _, ok := got[missing]; ok {
		t.Fatalf("expected missing user to be absent, got %+v", got[missing])
	}
}

func TestDualProvider_GetProfilesByUserID_SkipsSecondaryWhenPrimaryFullyResolves(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	primary := &fakeProvider{byID: map[uuid.UUID]Profile{id: {UserID: id, Email: "p@example.com"}}}
	secondary := &fakeProvider{}

	dual := newDualProvider(primary, secondary)
	if _, err := dual.GetProfilesByUserID(t.Context(), []uuid.UUID{id}); err != nil {
		t.Fatalf("GetProfilesByUserID() error = %v", err)
	}
	if secondary.calls != 0 {
		t.Fatalf("secondary calls = %d, want 0", secondary.calls)
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

func TestDualProvider_FindProfilesByEmail_DedupesByUserIDPrimaryWins(t *testing.T) {
	t.Parallel()

	sharedID := uuid.New()
	onlyInSecondaryID := uuid.New()

	primary := &fakeProvider{byEmail: map[string][]Profile{
		"shared@example.com": {{UserID: sharedID, Email: "shared@example.com", Name: "primary-name"}},
	}}
	secondary := &fakeProvider{byEmail: map[string][]Profile{
		"shared@example.com": {
			{UserID: sharedID, Email: "shared@example.com", Name: "secondary-name-loses"},
			{UserID: onlyInSecondaryID, Email: "shared@example.com", Name: "secondary-only"},
		},
	}}

	dual := newDualProvider(primary, secondary)
	got, err := dual.FindProfilesByEmail(t.Context(), "shared@example.com")
	if err != nil {
		t.Fatalf("FindProfilesByEmail() error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d profiles, want 2: %+v", len(got), got)
	}

	byID := make(map[uuid.UUID]Profile, len(got))
	for _, profile := range got {
		byID[profile.UserID] = profile
	}
	if byID[sharedID].Name != "primary-name" {
		t.Fatalf("shared id name = %q, want primary-name", byID[sharedID].Name)
	}
	if byID[onlyInSecondaryID].Name != "secondary-only" {
		t.Fatalf("secondary-only name = %q, want secondary-only", byID[onlyInSecondaryID].Name)
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
