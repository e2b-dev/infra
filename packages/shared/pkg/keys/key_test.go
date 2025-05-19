package keys

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskKey(t *testing.T) {
	t.Run("succeeds", func(t *testing.T) {
		masked, err := MaskKey("test_", "1234567890")
		assert.NoError(t, err)
		assert.Equal(t, "test_******7890", masked)
	})

	t.Run("too short key value", func(t *testing.T) {
		_, err := MaskKey("test", "123")
		assert.Error(t, err)
	})

	t.Run("empty prefix", func(t *testing.T) {
		masked, err := MaskKey("", "1234567890")
		assert.NoError(t, err)
		assert.Equal(t, "******7890", masked)
	})
}

func TestGenerateKey(t *testing.T) {
	t.Run("succeeds", func(t *testing.T) {
		key, err := GenerateKey("test_")
		assert.NoError(t, err)
		assert.Regexp(t, "^test_.*", key.PrefixedRawValue)
		assert.Regexp(t, "^test_\\*+[0-9a-f]{4}$", key.MaskedValue)
		assert.Regexp(t, "^\\$sha256\\$.*", key.HashedValue)
	})

	t.Run("no prefix", func(t *testing.T) {
		key, err := GenerateKey("")
		assert.NoError(t, err)
		assert.Regexp(t, "^[0-9a-f]{40}$", key.PrefixedRawValue)
		assert.Regexp(t, "^\\*+[0-9a-f]{4}$", key.MaskedValue)
		assert.Regexp(t, "^\\$sha256\\$.*", key.HashedValue)
	})
}

func TestGetMaskedIdentifierProperties(t *testing.T) {
	type testCase struct {
		name              string
		prefix            string
		identifier        string // full identifier including the key prefix
		expectedResult    MaskedIdentifier
		expectedErrString string
	}

	testCases := []testCase{
		// --- ERROR CASES (identifierValue's length <= identifierValueSuffixLength) ---
		{
			name:              "error when value length is less than suffix length", // value "abc" (len 3), suffixLen (4)
			prefix:            "pk_",
			identifier:        "pk_abc",
			expectedResult:    MaskedIdentifier{},
			expectedErrString: fmt.Sprintf("mask value length is less than or equal to identifier suffix length (%d), which would expose the entire identifier (%d)", identifierValueSuffixLength, 3),
		},
		{
			name:              "error when value length equals suffix length", // value "abcd" (len 4), suffixLen (4)
			prefix:            "sk_",
			identifier:        "sk_abcd",
			expectedResult:    MaskedIdentifier{},
			expectedErrString: fmt.Sprintf("mask value length is less than or equal to identifier suffix length (%d), which would expose the entire identifier (%d)", identifierValueSuffixLength, 4),
		},
		{
			name:              "error when value is empty", // value "" (len 0), suffixLen (4)
			prefix:            "err_",
			identifier:        "err_",
			expectedResult:    MaskedIdentifier{},
			expectedErrString: fmt.Sprintf("mask value length is less than or equal to identifier suffix length (%d), which would expose the entire identifier (%d)", identifierValueSuffixLength, 0),
		},

		// --- SUCCESS CASES (identifierValue's length > identifierValueSuffixLength) ---
		{
			name:       "success: value long, prefix val len fully used",
			prefix:     "pk_",
			identifier: "pk_abcdefghij",
			expectedResult: MaskedIdentifier{
				Prefix:            "pk_",
				ValueLength:       10,     // len("abcdefghij")
				MaskedValuePrefix: "ab",   // "abcdefghij"[:2]
				MaskedValueSuffix: "ghij", // "abcdefghij"[6:]
			},
		},
		{
			name:       "success: value medium, prefix val len truncated by overlap with suffix",
			prefix:     "", // Using empty key prefix
			identifier: "abcde",
			expectedResult: MaskedIdentifier{
				Prefix:            "",
				ValueLength:       5,      // len("abcde")
				MaskedValuePrefix: "a",    // "abcde"[:1]
				MaskedValueSuffix: "bcde", // "abcde"[1:]
			},
		},
		{
			name:       "success: value medium, prefix val len fits exactly before suffix",
			prefix:     "pk_",
			identifier: "pk_abcdef",
			expectedResult: MaskedIdentifier{
				Prefix:            "pk_",
				ValueLength:       6,      // len("abcdef")
				MaskedValuePrefix: "ab",   // "abcdef"[:2]
				MaskedValueSuffix: "cdef", // "abcdef"[2:]
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := GetMaskedIdentifierProperties(tc.prefix, tc.identifier)

			if tc.expectedErrString != "" {
				assert.Error(t, err)
				assert.EqualError(t, err, tc.expectedErrString)
				assert.Equal(t, tc.expectedResult, result) // Expect zero value for MaskedIdentifier on error
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedResult, result)
			}
		})
	}
}
