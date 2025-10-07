package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateHostname(t *testing.T) {
	t.Run("valid hostnames", func(t *testing.T) {
		validCases := []string{
			"example.com",
			"sub.example.com",
			"sub-domain.example.com",
			"example123.com",
			"123-example.com",
			"a.b.c.d.example.com",
			"api.example.com",
			"api-v2.example.com",
			"1.2.3.4", // IP-like patterns are valid hostnames
			"localhost",
			"a.co",
		}

		for _, hostname := range validCases {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.NoError(t, err, "hostname %q should be valid", hostname)
			})
		}
	})

	t.Run("valid wildcard patterns", func(t *testing.T) {
		validWildcards := []string{
			"*",
			"*.example.com",
			"*.*.example.com",
			"api.*.example.com",
			"*.*",
			"*.*.*.example.com",
		}

		for _, hostname := range validWildcards {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.NoError(t, err, "wildcard pattern %q should be valid", hostname)
			})
		}
	})

	t.Run("mixed wildcard with other characters in same label is invalid", func(t *testing.T) {
		invalidMixed := []string{
			"*-service.example.com",
			"api-*.example.com",
			"*api.example.com",
			"api*.example.com",
		}

		for _, hostname := range invalidMixed {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with mixed wildcard should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("glob patterns with question marks are invalid", func(t *testing.T) {
		invalidGlobs := []string{
			"api-?.test.com",
			"example?.com",
			"?example.com",
		}

		for _, hostname := range invalidGlobs {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				require.Error(t, err, "hostname %q with question mark should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("glob patterns with brackets are invalid", func(t *testing.T) {
		invalidGlobs := []string{
			"service[1-3].example.com",
			"host[abc].example.com",
			"[invalid-pattern",
			"host[1-3.example.com", // unclosed bracket
		}

		for _, hostname := range invalidGlobs {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				require.Error(t, err, "hostname %q with brackets should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("URL schemes are invalid", func(t *testing.T) {
		invalidSchemes := []string{
			"https://example.com",
			"http://example.com",
			"ftp://example.com",
			"ws://example.com",
		}

		for _, hostname := range invalidSchemes {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with scheme should be invalid", hostname)
				assert.Contains(t, err.Error(), "cannot contain schemes")
			})
		}
	})

	t.Run("URLs with paths are invalid", func(t *testing.T) {
		invalidPaths := []string{
			"example.com/api",
			"example.com/api/v1",
			"api.example.com/endpoint",
		}

		for _, hostname := range invalidPaths {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with path should be invalid", hostname)
				assert.Contains(t, err.Error(), "cannot contain schemes")
			})
		}
	})

	t.Run("ports are invalid", func(t *testing.T) {
		invalidPorts := []string{
			"example.com:8080",
			"localhost:3000",
			"api.example.com:443",
		}

		for _, hostname := range invalidPorts {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with port should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("invalid characters", func(t *testing.T) {
		invalidChars := []string{
			"example!.com",
			"example@.com",
			"example#.com",
			"example$.com",
			"example%.com",
			"example^.com",
			"example&.com",
			"example(.com",
			"example).com",
			"example+.com",
			"example=.com",
			"example_.com", // underscore is actually invalid in hostnames (valid in DNS but not in URLs)
			"host\\example.com",
		}

		for _, hostname := range invalidChars {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with special characters should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("whitespace is invalid", func(t *testing.T) {
		invalidWhitespace := []string{
			"example .com",
			" example.com",
			"example.com ",
			"example\t.com",
			"example\n.com",
		}

		for _, hostname := range invalidWhitespace {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with whitespace should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("labels cannot start with hyphen", func(t *testing.T) {
		invalidHyphens := []string{
			"-example.com",
			"sub.-example.com",
			"api.-test.com",
		}

		for _, hostname := range invalidHyphens {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				require.Error(t, err, "hostname %q with label starting with hyphen should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("labels cannot end with hyphen", func(t *testing.T) {
		invalidHyphens := []string{
			"example-.com",
			"sub.example-.com",
			"api-test-.com",
		}

		for _, hostname := range invalidHyphens {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with label ending with hyphen should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("multiple consecutive dots are invalid", func(t *testing.T) {
		invalidDots := []string{
			"example..com",
			"sub...example.com",
			"api..test..com",
		}

		for _, hostname := range invalidDots {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with consecutive dots should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("leading dot is invalid", func(t *testing.T) {
		invalidDots := []string{
			".example.com",
			".com",
			".api.test.com",
		}

		for _, hostname := range invalidDots {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with leading dot should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("trailing dot is invalid", func(t *testing.T) {
		invalidDots := []string{
			"example.com.",
			"api.example.com.",
		}

		for _, hostname := range invalidDots {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q with trailing dot should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("empty string is invalid", func(t *testing.T) {
		err := validateHostname("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid hostname pattern")
	})

	t.Run("only dots are invalid", func(t *testing.T) {
		invalidDots := []string{
			".",
			"..",
			"...",
		}

		for _, hostname := range invalidDots {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.Error(t, err, "hostname %q should be invalid", hostname)
				assert.Contains(t, err.Error(), "invalid hostname pattern")
			})
		}
	})

	t.Run("single character labels are valid", func(t *testing.T) {
		validSingle := []string{
			"a.b.c",
			"x.y.z.example.com",
		}

		for _, hostname := range validSingle {
			t.Run(hostname, func(t *testing.T) {
				err := validateHostname(hostname)
				assert.NoError(t, err, "hostname %q with single character labels should be valid", hostname)
			})
		}
	})

	t.Run("very long label is invalid", func(t *testing.T) {
		// DNS labels have a maximum length of 63 characters
		longLabel := ""
		for range 64 {
			longLabel += "a"
		}
		hostname := longLabel + ".example.com"

		err := validateHostname(hostname)
		assert.Error(t, err, "hostname with 64-character label should be invalid")
		assert.Contains(t, err.Error(), "invalid hostname pattern")
	})

	t.Run("label with exactly 63 characters is valid", func(t *testing.T) {
		// DNS labels can be up to 63 characters
		maxLabel := ""
		for range 63 {
			maxLabel += "a"
		}
		hostname := maxLabel + ".example.com"

		err := validateHostname(hostname)
		assert.NoError(t, err, "hostname with 63-character label should be valid")
	})

	t.Run("wildcard only should return early", func(t *testing.T) {
		err := validateHostname("*")
		assert.NoError(t, err, "single wildcard should be valid")
	})
}
