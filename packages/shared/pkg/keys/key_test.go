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
		identifier        string
		expectedResult    MaskedIdentifier
		expectedErrString string
	}

	testCases := []testCase{
		{
			name:       "succeeds with long identifier",
			prefix:     "pk_",
			identifier: "pk_abcdefghij", // value "abcdefghij", len 10
			expectedResult: MaskedIdentifier{
				Prefix:            "pk_",
				ValueLength:       10,
				MaskedValuePrefix: "abcdefgh", // value[:10-2]
				MaskedValueSuffix: "ghij",     // value[10-4:]
			},
			expectedErrString: "",
		},
		{
			name:       "succeeds with identifier length equal to suffix display length",
			prefix:     "sk_",
			identifier: "sk_abcd", // value "abcd", len 4
			expectedResult: MaskedIdentifier{
				Prefix:            "sk_",
				ValueLength:       4,
				MaskedValuePrefix: "ab",   // value[:4-2]
				MaskedValueSuffix: "abcd", // value[4-4:]
			},
			expectedErrString: "",
		},
		{
			name:       "succeeds with identifier length slightly longer than suffix display length and empty prefix",
			prefix:     "",
			identifier: "abcde", // value "abcde", len 5
			expectedResult: MaskedIdentifier{
				Prefix:            "",
				ValueLength:       5,
				MaskedValuePrefix: "abc",  // value[:5-2]
				MaskedValueSuffix: "bcde", // value[5-4:]
			},
			expectedErrString: "",
		},
		{
			name:              "fails when identifier value is too short",
			prefix:            "err_",
			identifier:        "err_abc", // value "abc", len 3
			expectedResult:    MaskedIdentifier{},
			expectedErrString: fmt.Sprintf("mask value length is less than key suffix length (%d)", identifierValueSuffixLength),
		},
		{
			name:              "fails with empty prefix and identifier value too short",
			prefix:            "",
			identifier:        "ab", // value "ab", len 2
			expectedResult:    MaskedIdentifier{},
			expectedErrString: fmt.Sprintf("mask value length is less than key suffix length (%d)", identifierValueSuffixLength),
		},
		{
			name:              "fails when identifier value is empty (identifier matches prefix)",
			prefix:            "onlyprefix_",
			identifier:        "onlyprefix_", // value "", len 0
			expectedResult:    MaskedIdentifier{},
			expectedErrString: fmt.Sprintf("mask value length is less than key suffix length (%d)", identifierValueSuffixLength),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := GetMaskedIdentifierProperties(tc.prefix, tc.identifier)

			if tc.expectedErrString != "" {
				assert.Error(t, err)
				assert.EqualError(t, err, tc.expectedErrString)
				assert.Equal(t, tc.expectedResult, result) // Expect zero value for result on error
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedResult, result)
			}
		})
	}
}
