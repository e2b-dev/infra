package id

import (
	"testing"
)

func TestParseTemplateIDWithTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		wantTemplateID string
		wantTag        *string
		wantErr        bool
	}{
		{
			name:           "template ID only",
			input:          "my-template",
			wantTemplateID: "my-template",
			wantTag:        nil,
			wantErr:        false,
		},
		{
			name:           "template ID with tag",
			input:          "my-template:v1.0",
			wantTemplateID: "my-template",
			wantTag:        stringPtr("v1.0"),
			wantErr:        false,
		},
		{
			name:           "template ID with underscores",
			input:          "my_template_123:prod",
			wantTemplateID: "my_template_123",
			wantTag:        stringPtr("prod"),
			wantErr:        false,
		},
		{
			name:           "uppercase converted to lowercase",
			input:          "MyTemplate:Prod",
			wantTemplateID: "mytemplate",
			wantTag:        stringPtr("prod"),
			wantErr:        false,
		},
		{
			name:           "with spaces trimmed",
			input:          "  my-template  :  v1  ",
			wantTemplateID: "my-template",
			wantTag:        stringPtr("v1"),
			wantErr:        false,
		},
		{
			name:           "invalid characters in template ID",
			input:          "my template!:v1",
			wantTemplateID: "",
			wantTag:        nil,
			wantErr:        true,
		},
		{
			name:           "invalid characters in tag",
			input:          "my-template:v1.0!",
			wantTemplateID: "",
			wantTag:        nil,
			wantErr:        true,
		},
		{
			name:           "empty template ID",
			input:          ":tag",
			wantTemplateID: "",
			wantTag:        nil,
			wantErr:        true,
		},
		{
			name:           "empty tag after colon",
			input:          "my-template:",
			wantTemplateID: "",
			wantTag:        nil,
			wantErr:        true,
		},
		{
			name:           "default tag normalized to nil",
			input:          "my-template:default",
			wantTemplateID: "my-template",
			wantTag:        nil,
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotTemplateID, gotTag, err := ParseTemplateIDOrAliasWithTag(tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTemplateIDWithTag() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if gotTemplateID != tt.wantTemplateID {
				t.Errorf("ParseTemplateIDWithTag() gotTemplateID = %v, want %v", gotTemplateID, tt.wantTemplateID)
			}

			if (gotTag == nil) != (tt.wantTag == nil) {
				t.Errorf("ParseTemplateIDWithTag() gotTag = %v, want %v", gotTag, tt.wantTag)

				return
			}

			if gotTag != nil && tt.wantTag != nil && *gotTag != *tt.wantTag {
				t.Errorf("ParseTemplateIDWithTag() gotTag = %v, want %v", *gotTag, *tt.wantTag)
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestValidateAndDeduplicateTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   []string
		want    []string
		wantErr bool
	}{
		{
			name:    "valid tags normalized to lowercase",
			input:   []string{"V1", "Prod"},
			want:    []string{"v1", "prod"},
			wantErr: false,
		},
		{
			name:    "duplicates removed",
			input:   []string{"v1", "v1", "v2"},
			want:    []string{"v1", "v2"},
			wantErr: false,
		},
		{
			name:    "case-insensitive deduplication",
			input:   []string{"V1", "v1", "V1"},
			want:    []string{"v1"},
			wantErr: false,
		},
		{
			name:    "whitespace trimmed",
			input:   []string{"  v1  ", "  v2  "},
			want:    []string{"v1", "v2"},
			wantErr: false,
		},
		{
			name:    "empty input returns empty slice",
			input:   []string{},
			want:    []string{},
			wantErr: false,
		},
		{
			name:    "invalid tag with special characters",
			input:   []string{"v1", "invalid!tag"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "UUID tag rejected",
			input:   []string{"550e8400-e29b-41d4-a716-446655440000"},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateAndDeduplicateTags(tt.input)

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
