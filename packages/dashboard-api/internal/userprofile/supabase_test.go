package userprofile

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

	supabasequeries "github.com/e2b-dev/infra/packages/db/pkg/supabase/queries"
)

func TestProfileFromAuthUserNamePrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		user supabasequeries.AuthUser
		want string
	}{
		{
			name: "first and last name",
			user: supabasequeries.AuthUser{
				ID:              uuid.New(),
				Email:           "fallback@example.com",
				RawUserMetaData: []byte(`{"first_name":"ada","last_name":"lovelace","username":"fallback user"}`),
			},
			want: "ada lovelace",
		},
		{
			name: "full name fallback",
			user: supabasequeries.AuthUser{
				ID:              uuid.New(),
				Email:           "fallback@example.com",
				RawUserMetaData: []byte(`{"full_name":"grace hopper"}`),
			},
			want: "grace hopper",
		},
		{
			name: "username is not profile name",
			user: supabasequeries.AuthUser{
				ID:              uuid.New(),
				Email:           "fallback@example.com",
				RawUserMetaData: []byte(`{"username":"john doe"}`),
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := profileFromAuthUser(tt.user)
			if got.Name != tt.want {
				t.Fatalf("profileFromAuthUser().Name = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

func TestProfileFromAuthUserProviders(t *testing.T) {
	t.Parallel()

	user := supabasequeries.AuthUser{
		ID:             uuid.New(),
		Email:          "ada@example.com",
		RawAppMetaData: []byte(`{"providers":["github"," email ","GOOGLE","apple","github"],"provider":"google"}`),
	}

	got := profileFromAuthUser(user)
	want := []string{"email", "google", "github"}
	if !reflect.DeepEqual(got.Providers, want) {
		t.Fatalf("profileFromAuthUser().Providers = %v, want %v", got.Providers, want)
	}
}
