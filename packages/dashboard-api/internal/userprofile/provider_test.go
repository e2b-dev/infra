package userprofile

import (
	"context"
	"testing"

	"github.com/google/uuid"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

type fakeProvider struct{}

func (f *fakeProvider) GetProfilesByUserID(_ context.Context, _ []uuid.UUID) (map[uuid.UUID]Profile, error) {
	return nil, nil
}

func (f *fakeProvider) FindProfilesByEmail(_ context.Context, _ string) ([]Profile, error) {
	return nil, nil
}

func (f *fakeProvider) GetTeamCreatorContext(_ context.Context, _ uuid.UUID) (*sharedteamprovision.CreatorContextV1, error) {
	return nil, nil
}

func TestNewProvider_FactorySelection(t *testing.T) {
	t.Parallel()

	supa := &fakeProvider{}
	ory := &fakeProvider{}

	tests := []struct {
		name    string
		mode    Mode
		supa    Provider
		ory     Provider
		want    Provider
		wantErr bool
	}{
		{name: "supabase mode returns supabase", mode: ModeSupabase, supa: supa, ory: ory, want: supa},
		{name: "ory mode returns ory", mode: ModeOry, supa: supa, ory: ory, want: ory},
		{name: "supabase mode requires supabase", mode: ModeSupabase, ory: ory, wantErr: true},
		{name: "ory mode requires ory", mode: ModeOry, supa: supa, wantErr: true},
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
			if got != tt.want {
				t.Fatalf("NewProvider() = %T, want %T", got, tt.want)
			}
		})
	}
}
