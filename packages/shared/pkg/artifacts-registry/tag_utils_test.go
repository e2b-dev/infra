package artifacts_registry

import (
	"strings"
	"testing"
)

func TestValidateTemplateId(t *testing.T) {
	tests := []struct {
		name        string
		templateId  string
		expectError bool
		errorType   error
	}{
		{
			name:        "valid alphanumeric",
			templateId:  "template123",
			expectError: false,
		},
		{
			name:        "valid with hyphens",
			templateId:  "template-123",
			expectError: false,
		},
		{
			name:        "valid with underscores",
			templateId:  "template_123",
			expectError: false,
		},
		{
			name:        "valid mixed",
			templateId:  "my-template_123",
			expectError: false,
		},
		{
			name:        "empty string",
			templateId:  "",
			expectError: true,
			errorType:   ErrEmptyTemplateId,
		},
		{
			name:        "invalid characters - spaces",
			templateId:  "template 123",
			expectError: true,
			errorType:   ErrInvalidTemplateId,
		},
		{
			name:        "invalid characters - special chars",
			templateId:  "template@123",
			expectError: true,
			errorType:   ErrInvalidTemplateId,
		},
		{
			name:        "invalid characters - forward slash",
			templateId:  "template/123",
			expectError: true,
			errorType:   ErrInvalidTemplateId,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTemplateId(tt.templateId)
			
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errorType != nil && !strings.Contains(err.Error(), tt.errorType.Error()) {
					t.Errorf("expected error type %v, got %v", tt.errorType, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidateBuildId(t *testing.T) {
	tests := []struct {
		name        string
		buildId     string
		expectError bool
		errorType   error
	}{
		{
			name:        "valid alphanumeric",
			buildId:     "build123",
			expectError: false,
		},
		{
			name:        "valid with hyphens",
			buildId:     "build-123",
			expectError: false,
		},
		{
			name:        "valid with underscores",
			buildId:     "build_123",
			expectError: false,
		},
		{
			name:        "valid UUID-like",
			buildId:     "550e8400-e29b-41d4-a716-446655440000",
			expectError: false,
		},
		{
			name:        "empty string",
			buildId:     "",
			expectError: true,
			errorType:   ErrEmptyBuildId,
		},
		{
			name:        "invalid characters - spaces",
			buildId:     "build 123",
			expectError: true,
			errorType:   ErrInvalidBuildId,
		},
		{
			name:        "invalid characters - special chars",
			buildId:     "build@123",
			expectError: true,
			errorType:   ErrInvalidBuildId,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBuildId(tt.buildId)
			
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errorType != nil && !strings.Contains(err.Error(), tt.errorType.Error()) {
					t.Errorf("expected error type %v, got %v", tt.errorType, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestGenerateCompositeTag(t *testing.T) {
	tests := []struct {
		name        string
		templateId  string
		buildId     string
		expected    string
		expectError bool
		errorType   error
	}{
		{
			name:       "valid simple case",
			templateId: "template1",
			buildId:    "build1",
			expected:   "template1_build1",
		},
		{
			name:       "valid with hyphens",
			templateId: "my-template",
			buildId:    "my-build",
			expected:   "my-template_my-build",
		},
		{
			name:       "valid with underscores",
			templateId: "my_template",
			buildId:    "my_build",
			expected:   "my_template_my_build",
		},
		{
			name:        "invalid template id",
			templateId:  "template@123",
			buildId:     "build1",
			expectError: true,
			errorType:   ErrInvalidTemplateId,
		},
		{
			name:        "invalid build id",
			templateId:  "template1",
			buildId:     "build@123",
			expectError: true,
			errorType:   ErrInvalidBuildId,
		},
		{
			name:        "empty template id",
			templateId:  "",
			buildId:     "build1",
			expectError: true,
			errorType:   ErrEmptyTemplateId,
		},
		{
			name:        "empty build id",
			templateId:  "template1",
			buildId:     "",
			expectError: true,
			errorType:   ErrEmptyBuildId,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateCompositeTag(tt.templateId, tt.buildId)
			
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errorType != nil && !strings.Contains(err.Error(), tt.errorType.Error()) {
					t.Errorf("expected error type %v, got %v", tt.errorType, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if result != tt.expected {
					t.Errorf("expected %s, got %s", tt.expected, result)
				}
			}
		})
	}
}

func TestGenerateCompositeTagWithOptions(t *testing.T) {
	tests := []struct {
		name        string
		templateId  string
		buildId     string
		options     CompositeTagOptions
		expectError bool
		errorType   error
		validate    func(t *testing.T, result string)
	}{
		{
			name:       "normal length with TruncateNone",
			templateId: "template",
			buildId:    "build",
			options: CompositeTagOptions{
				MaxLength:        128,
				TruncateStrategy: TruncateNone,
			},
			validate: func(t *testing.T, result string) {
				if result != "template_build" {
					t.Errorf("expected 'template_build', got '%s'", result)
				}
			},
		},
		{
			name:       "too long with TruncateNone",
			templateId: "verylongtemplatename",
			buildId:    "verylongbuildname",
			options: CompositeTagOptions{
				MaxLength:        20,
				TruncateStrategy: TruncateNone,
			},
			expectError: true,
			errorType:   ErrTagTooLong,
		},
		{
			name:       "too long with TruncateEnd",
			templateId: "verylongtemplatename",
			buildId:    "verylongbuildname",
			options: CompositeTagOptions{
				MaxLength:        20,
				TruncateStrategy: TruncateEnd,
			},
			validate: func(t *testing.T, result string) {
				if len(result) != 20 {
					t.Errorf("expected length 20, got %d", len(result))
				}
				if !strings.HasPrefix(result, "verylongtemplatename") {
					t.Errorf("expected to start with template name, got '%s'", result)
				}
			},
		},
		{
			name:       "too long with TruncateMiddle",
			templateId: "verylongtemplatename",
			buildId:    "verylongbuildname",
			options: CompositeTagOptions{
				MaxLength:        20,
				TruncateStrategy: TruncateMiddle,
			},
			validate: func(t *testing.T, result string) {
				if len(result) != 20 {
					t.Errorf("expected length 20, got %d", len(result))
				}
				if !strings.Contains(result, "_") {
					t.Errorf("expected to contain separator, got '%s'", result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateCompositeTagWithOptions(tt.templateId, tt.buildId, tt.options)
			
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errorType != nil && !strings.Contains(err.Error(), tt.errorType.Error()) {
					t.Errorf("expected error type %v, got %v", tt.errorType, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

func TestParseCompositeTag(t *testing.T) {
	tests := []struct {
		name           string
		compositeTag   string
		expectedTmplId string
		expectedBldId  string
		expectError    bool
	}{
		{
			name:           "valid simple case",
			compositeTag:   "template1_build1",
			expectedTmplId: "template1",
			expectedBldId:  "build1",
		},
		{
			name:           "valid with multiple separators",
			compositeTag:   "my-template_my-build",
			expectedTmplId: "my-template",
			expectedBldId:  "my-build",
		},
		{
			name:           "valid with underscores",
			compositeTag:   "my-template-my_build",
			expectedTmplId: "my-template-my",
			expectedBldId:  "build",
		},
		{
			name:        "empty tag",
			compositeTag: "",
			expectError: true,
		},
		{
			name:        "no separator",
			compositeTag: "templatebuild",
			expectError: true,
		},
		{
			name:        "empty template part",
			compositeTag: "-build1",
			expectError: true,
		},
		{
			name:        "empty build part",
			compositeTag: "template1-",
			expectError: true,
		},
		{
			name:        "only separator",
			compositeTag: "-",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templateId, buildId, err := ParseCompositeTag(tt.compositeTag)
			
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if templateId != tt.expectedTmplId {
					t.Errorf("expected template id %s, got %s", tt.expectedTmplId, templateId)
				}
				if buildId != tt.expectedBldId {
					t.Errorf("expected build id %s, got %s", tt.expectedBldId, buildId)
				}
			}
		})
	}
}

func TestIsCompositeTag(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		expected bool
	}{
		{
			name:     "valid composite tag",
			tag:      "template1_build1",
			expected: true,
		},
		{
			name:     "valid with multiple separators",
			tag:      "my-template_my-build",
			expected: true,
		},
		{
			name:     "invalid - no separator",
			tag:      "templatebuild",
			expected: false,
		},
		{
			name:     "invalid - empty",
			tag:      "",
			expected: false,
		},
		{
			name:     "invalid - only separator",
			tag:      "-",
			expected: false,
		},
		{
			name:     "invalid - empty parts",
			tag:      "-build",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCompositeTag(tt.tag)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestTruncateStrategies(t *testing.T) {
	t.Run("truncateEnd", func(t *testing.T) {
		result := truncateEnd("verylongstring", 10)
		if result != "verylongst" {
			t.Errorf("expected 'verylongst', got '%s'", result)
		}
		if len(result) != 10 {
			t.Errorf("expected length 10, got %d", len(result))
		}
	})

	t.Run("truncateMiddle", func(t *testing.T) {
		result := truncateMiddle("verylongtemplate", "verylongbuild", 20)
		if len(result) != 20 {
			t.Errorf("expected length 20, got %d", len(result))
		}
		if !strings.Contains(result, "_") {
			t.Errorf("expected to contain separator, got '%s'", result)
		}
		
		// Should preserve some part of both template and build
		parts := strings.Split(result, "_")
		if len(parts) != 2 {
			t.Errorf("expected exactly 2 parts, got %d", len(parts))
		}
		if len(parts[0]) == 0 || len(parts[1]) == 0 {
			t.Errorf("both parts should be non-empty, got '%s' and '%s'", parts[0], parts[1])
		}
	})
}

func TestTagLengthLimits(t *testing.T) {
	// Test with AWS ECR maximum length
	longTemplateId := strings.Repeat("a", 60)
	longBuildId := strings.Repeat("b", 60)
	
	// This should exceed the 128 character limit
	result, err := GenerateCompositeTag(longTemplateId, longBuildId)
	expectedLen := len(longTemplateId) + 1 + len(longBuildId) // 60 + 1 + 60 = 121, which is < 128
	t.Logf("Generated tag length: %d, content: %s", len(result), result)
	if expectedLen <= MaxTagLength {
		t.Logf("Tag length %d is within limit %d, no error expected", expectedLen, MaxTagLength)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	} else if err == nil {
		t.Errorf("expected error for tag exceeding length limit")
	}
	if err != nil && !strings.Contains(err.Error(), ErrTagTooLong.Error()) {
		t.Errorf("expected ErrTagTooLong, got %v", err)
	}
	
	// Test with truncation
	options := CompositeTagOptions{
		MaxLength:        MaxTagLength,
		TruncateStrategy: TruncateEnd,
	}
	
	result2, err := GenerateCompositeTagWithOptions(longTemplateId, longBuildId, options)
	if err != nil {
		t.Errorf("unexpected error with truncation: %v", err)
	}
	if len(result2) > MaxTagLength {
		t.Errorf("result length %d exceeds maximum %d", len(result2), MaxTagLength)
	}
}

func TestEdgeCases(t *testing.T) {
	t.Run("very short max length", func(t *testing.T) {
		options := CompositeTagOptions{
			MaxLength:        5,
			TruncateStrategy: TruncateMiddle,
		}
		
		result, err := GenerateCompositeTagWithOptions("template", "build", options)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(result) != 5 {
			t.Errorf("expected length 5, got %d", len(result))
		}
	})
	
	t.Run("max length equals separator", func(t *testing.T) {
		options := CompositeTagOptions{
			MaxLength:        1,
			TruncateStrategy: TruncateMiddle,
		}
		
		result, err := GenerateCompositeTagWithOptions("template", "build", options)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result != "_" {
			t.Errorf("expected '_', got '%s'", result)
		}
	})
}