package id

import (
	"testing"
)

func TestParseTemplateIDWithTag(t *testing.T) {
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
			name:           "latest tag normalized to nil",
			input:          "my-template:latest",
			wantTemplateID: "my-template",
			wantTag:        nil,
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
