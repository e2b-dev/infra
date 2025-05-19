package keys

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskKey(t *testing.T) {
	t.Run("succeeds: value longer than suffix length", func(t *testing.T) {
		masked, err := MaskKey("test_", "1234567890") // len 10, suffixLen 4
		assert.NoError(t, err)
		assert.Equal(t, "test_******7890", masked)
	})

	t.Run("succeeds: empty prefix, value longer than suffix length", func(t *testing.T) {
		masked, err := MaskKey("", "1234567890") // len 10, suffixLen 4
		assert.NoError(t, err)
		assert.Equal(t, "******7890", masked)
	})

	t.Run("error: value length less than suffix length", func(t *testing.T) {
		_, err := MaskKey("test", "123") // len 3, suffixLen 4
		assert.Error(t, err)
		assert.EqualError(t, err, fmt.Sprintf("mask value length is less than or equal to key suffix length (%d)", identifierValueSuffixLength))
	})

	t.Run("error: value length equals suffix length", func(t *testing.T) {
		_, err := MaskKey("test", "1234") // len 4, suffixLen 4
		assert.Error(t, err)
		assert.EqualError(t, err, fmt.Sprintf("mask value length is equal to key suffix length (%d), which would expose the entire key in the mask", identifierValueSuffixLength))
	})
}

func TestGenerateKey(t *testing.T) {
	t.Run("succeeds", func(t *testing.T) {
		key, err := GenerateKey("test_")
		assert.NoError(t, err)
		assert.Regexp(t, "^test_.*", key.PrefixedRawValue)
		assert.Regexp(t, `^test_\*+[0-9a-f]{4}$`, key.MaskedValue)
		assert.Regexp(t, "^\\$sha256\\$.*", key.HashedValue)
	})

	t.Run("no prefix", func(t *testing.T) {
		key, err := GenerateKey("")
		assert.NoError(t, err)
		assert.Regexp(t, "^[0-9a-f]{40}$", key.PrefixedRawValue)
		assert.Regexp(t, `^\*+[0-9a-f]{4}$`, key.MaskedValue)
		assert.Regexp(t, "^\\$sha256\\$.*", key.HashedValue)
	})
}

func TestGetMaskedIdentifierProperties(t *testing.T) {
	type testCase struct {
		name              string
		prefix            string
		value             string
		expectedResult    MaskedIdentifier
		expectedErrString string
	}

	testCases := []testCase{
		// --- ERROR CASES (value's length <= identifierValueSuffixLength) ---
		{
			name:              "error: value length < suffix length (3 vs 4)",
			prefix:            "pk_",
			value:             "abc",
			expectedResult:    MaskedIdentifier{},
			expectedErrString: fmt.Sprintf("mask value length is less than identifier suffix length (%d)", identifierValueSuffixLength),
		},
		{
			name:              "error: value length == suffix length (4 vs 4)",
			prefix:            "sk_",
			value:             "abcd",
			expectedResult:    MaskedIdentifier{},
			expectedErrString: fmt.Sprintf("mask value length is equal to identifier suffix length (%d), which would expose the entire identifier in the mask", identifierValueSuffixLength),
		},
		{
			name:              "error: value length < suffix length (0 vs 4, empty value)",
			prefix:            "err_",
			value:             "",
			expectedResult:    MaskedIdentifier{},
			expectedErrString: fmt.Sprintf("mask value length is less than identifier suffix length (%d)", identifierValueSuffixLength),
		},

		// --- SUCCESS CASES (value's length > identifierValueSuffixLength) ---
		{
			name:   "success: value long (10), prefix val len fully used",
			prefix: "pk_",
			value:  "abcdefghij",
			expectedResult: MaskedIdentifier{
				Prefix:            "pk_",
				ValueLength:       10,
				MaskedValuePrefix: "ab",
				MaskedValueSuffix: "ghij",
			},
		},
		{
			name:   "success: value medium (5), prefix val len truncated by overlap",
			prefix: "",
			value:  "abcde",
			expectedResult: MaskedIdentifier{
				Prefix:            "",
				ValueLength:       5,
				MaskedValuePrefix: "a",
				MaskedValueSuffix: "bcde",
			},
		},
		{
			name:   "success: value medium (6), prefix val len fits exactly",
			prefix: "pk_",
			value:  "abcdef",
			expectedResult: MaskedIdentifier{
				Prefix:            "pk_",
				ValueLength:       6,
				MaskedValuePrefix: "ab",
				MaskedValueSuffix: "cdef",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Call GetMaskedIdentifierProperties with tc.prefix and tc.value
			result, err := GetMaskedIdentifierProperties(tc.prefix, tc.value)

			if tc.expectedErrString != "" {
				assert.Error(t, err)
				assert.EqualError(t, err, tc.expectedErrString)
				assert.Equal(t, tc.expectedResult, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedResult, result)
			}
		})
	}
}
