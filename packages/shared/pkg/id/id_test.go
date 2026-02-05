package id

import (
	"testing"

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

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseName() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if tt.wantErr {
				return
			}

			if gotIdentifier != tt.wantIdentifier {
				t.Errorf("ParseName() identifier = %v, want %v", gotIdentifier, tt.wantIdentifier)
			}

			if (gotTag == nil) != (tt.wantTag == nil) {
				t.Errorf("ParseName() tag = %v, want %v", gotTag, tt.wantTag)

				return
			}
			if gotTag != nil && tt.wantTag != nil && *gotTag != *tt.wantTag {
				t.Errorf("ParseName() tag = %v, want %v", *gotTag, *tt.wantTag)
			}
		})
	}
}

func TestWithNamespace(t *testing.T) {
	t.Parallel()

	got := WithNamespace("acme", "my-template")
	want := "acme/my-template"
	if got != want {
		t.Errorf("WithNamespace() = %q, want %q", got, want)
	}
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

			if (gotNamespace == nil) != (tt.wantNamespace == nil) {
				t.Errorf("SplitIdentifier() namespace = %v, want %v", gotNamespace, tt.wantNamespace)

				return
			}
			if gotNamespace != nil && tt.wantNamespace != nil && *gotNamespace != *tt.wantNamespace {
				t.Errorf("SplitIdentifier() namespace = %q, want %q", *gotNamespace, *tt.wantNamespace)
			}
			if gotAlias != tt.wantAlias {
				t.Errorf("SplitIdentifier() alias = %q, want %q", gotAlias, tt.wantAlias)
			}
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

			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAndDeduplicateTags() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if tt.wantErr {
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("ValidateAndDeduplicateTags() got %v, want %v", got, tt.want)

				return
			}

			// Check all expected tags are present (order may vary due to map iteration)
			gotSet := make(map[string]bool)
			for _, tag := range got {
				gotSet[tag] = true
			}
			for _, tag := range tt.want {
				if !gotSet[tag] {
					t.Errorf("ValidateAndDeduplicateTags() missing tag %v, got %v", tag, got)
				}
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
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNamespaceMatchesTeam() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
