package id

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestParseName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		wantIdentifier string
		wantTag        *string
		wantErr        bool
	}{
		{
			name:           "bare alias only",
			input:          "my-template",
			wantIdentifier: "my-template",
			wantTag:        nil,
		},
		{
			name:           "alias with tag",
			input:          "my-template:v1",
			wantIdentifier: "my-template",
			wantTag:        utils.ToPtr("v1"),
		},
		{
			name:           "namespace and alias",
			input:          "acme/my-template",
			wantIdentifier: "acme/my-template",
			wantTag:        nil,
		},
		{
			name:           "namespace, alias and tag",
			input:          "acme/my-template:v1",
			wantIdentifier: "acme/my-template",
			wantTag:        utils.ToPtr("v1"),
		},
		{
			name:           "namespace with hyphens",
			input:          "my-team/my-template:prod",
			wantIdentifier: "my-team/my-template",
			wantTag:        utils.ToPtr("prod"),
		},
		{
			name:           "default tag normalized to nil",
			input:          "my-template:default",
			wantIdentifier: "my-template",
			wantTag:        nil,
		},
		{
			name:           "uppercase converted to lowercase",
			input:          "MyTemplate:Prod",
			wantIdentifier: "mytemplate",
			wantTag:        utils.ToPtr("prod"),
		},
		{
			name:           "whitespace trimmed",
			input:          "  my-template  :  v1  ",
			wantIdentifier: "my-template",
			wantTag:        utils.ToPtr("v1"),
		},
		{
			name:    "invalid - empty namespace",
			input:   "/my-template",
			wantErr: true,
		},
		{
			name:    "invalid - empty tag after colon",
			input:   "my-template:",
			wantErr: true,
		},
		{
			name:    "invalid - special characters in alias",
			input:   "my template!",
			wantErr: true,
		},
		{
			name:    "invalid - special characters in namespace",
			input:   "my team!/my-template",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotIdentifier, gotTag, err := ParseName(tt.input)

			if tt.wantErr {
				require.Error(t, err, "Expected ParseName() to return error, got")

				return
			}

			require.NoError(t, err, "Expected ParseName() not to return error, got: %v", err)
			assert.Equal(t, tt.wantIdentifier, gotIdentifier, "ParseName() identifier = %v, want %v", gotIdentifier, tt.wantIdentifier)
			assert.Equal(t, tt.wantTag, gotTag, "ParseName() tag = %v, want %v", utils.Sprintp(gotTag), utils.Sprintp(tt.wantTag))
		})
	}
}

func TestWithNamespace(t *testing.T) {
	t.Parallel()

	got := WithNamespace("acme", "my-template")
	want := "acme/my-template"
	assert.Equal(t, want, got, "WithNamespace() = %q, want %q", got, want)
}

func TestSplitIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		identifier    string
		wantNamespace *string
		wantAlias     string
	}{
		{
			name:          "bare alias",
			identifier:    "my-template",
			wantNamespace: nil,
			wantAlias:     "my-template",
		},
		{
			name:          "with namespace",
			identifier:    "acme/my-template",
			wantNamespace: ptrStr("acme"),
			wantAlias:     "my-template",
		},
		{
			name:          "empty namespace prefix",
			identifier:    "/my-template",
			wantNamespace: ptrStr(""),
			wantAlias:     "my-template",
		},
		{
			name:          "multiple slashes - only first split",
			identifier:    "a/b/c",
			wantNamespace: ptrStr("a"),
			wantAlias:     "b/c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotNamespace, gotAlias := SplitIdentifier(tt.identifier)

			if tt.wantNamespace == nil {
				assert.Nil(t, gotNamespace)
			} else {
				require.NotNil(t, gotNamespace)
				assert.Equal(t, *tt.wantNamespace, *gotNamespace)
			}

			assert.Equal(t, tt.wantAlias, gotAlias)
		})
	}
}

func ptrStr(s string) *string {
	return &s
}

func TestValidateAndDeduplicateTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tags    []string
		want    []string
		wantErr bool
	}{
		{
			name:    "single valid tag",
			tags:    []string{"v1"},
			want:    []string{"v1"},
			wantErr: false,
		},
		{
			name:    "multiple unique tags",
			tags:    []string{"v1", "prod", "latest"},
			want:    []string{"v1", "prod", "latest"},
			wantErr: false,
		},
		{
			name:    "duplicate tags deduplicated",
			tags:    []string{"v1", "V1", "v1"},
			want:    []string{"v1"},
			wantErr: false,
		},
		{
			name:    "tags with dots and underscores",
			tags:    []string{"v1.0", "v1_1"},
			want:    []string{"v1.0", "v1_1"},
			wantErr: false,
		},
		{
			name:    "invalid - UUID tag rejected",
			tags:    []string{"550e8400-e29b-41d4-a716-446655440000"},
			wantErr: true,
		},
		{
			name:    "invalid - special characters",
			tags:    []string{"v1!", "v2@"},
			wantErr: true,
		},
		{
			name:    "empty list returns empty",
			tags:    []string{},
			want:    []string{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateAndDeduplicateTags(tt.tags)

			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

func TestValidateSandboxID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "canonical sandbox ID",
			input:   "i1a2b3c4d5e6f7g8h9j0k",
			wantErr: false,
		},
		{
			name:    "short alphanumeric",
			input:   "abc123",
			wantErr: false,
		},
		{
			name:    "all digits",
			input:   "1234567890",
			wantErr: false,
		},
		{
			name:    "all lowercase letters",
			input:   "abcdefghijklmnopqrst",
			wantErr: false,
		},
		{
			name:    "invalid - empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid - contains colon (Redis separator)",
			input:   "abc:def",
			wantErr: true,
		},
		{
			name:    "invalid - contains open brace (Redis hash slot)",
			input:   "abc{def",
			wantErr: true,
		},
		{
			name:    "invalid - contains close brace (Redis hash slot)",
			input:   "abc}def",
			wantErr: true,
		},
		{
			name:    "invalid - contains newline",
			input:   "abc\ndef",
			wantErr: true,
		},
		{
			name:    "invalid - contains space",
			input:   "abc def",
			wantErr: true,
		},
		{
			name:    "invalid - contains hyphen",
			input:   "abc-def",
			wantErr: true,
		},
		{
			name:    "invalid - contains uppercase",
			input:   "abcDEF",
			wantErr: true,
		},
		{
			name:    "invalid - contains slash",
			input:   "abc/def",
			wantErr: true,
		},
		{
			name:    "invalid - contains null byte",
			input:   "abc\x00def",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateSandboxID(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateNamespaceMatchesTeam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		identifier string
		teamSlug   string
		wantErr    bool
	}{
		{
			name:       "bare alias - no namespace",
			identifier: "my-template",
			teamSlug:   "acme",
			wantErr:    false,
		},
		{
			name:       "matching namespace",
			identifier: "acme/my-template",
			teamSlug:   "acme",
			wantErr:    false,
		},
		{
			name:       "mismatched namespace",
			identifier: "other-team/my-template",
			teamSlug:   "acme",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateNamespaceMatchesTeam(tt.identifier, tt.teamSlug)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
