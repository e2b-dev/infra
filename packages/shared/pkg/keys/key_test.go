package keys

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaskKey(t *testing.T) {
	t.Parallel()
	t.Run("succeeds: value longer than suffix length", func(t *testing.T) {
		t.Parallel()
		masked, err := MaskKey("test_", "1234567890")
		require.NoError(t, err)
		assert.Equal(t, "test_", masked.Prefix)
		assert.Equal(t, "12", masked.MaskedValuePrefix)
		assert.Equal(t, "7890", masked.MaskedValueSuffix)
	})

	t.Run("succeeds: empty prefix, value longer than suffix length", func(t *testing.T) {
		t.Parallel()
		masked, err := MaskKey("", "1234567890")
		require.NoError(t, err)
		assert.Empty(t, masked.Prefix)
		assert.Equal(t, "12", masked.MaskedValuePrefix)
		assert.Equal(t, "7890", masked.MaskedValueSuffix)
	})

	t.Run("error: value length less than suffix length", func(t *testing.T) {
		t.Parallel()
		_, err := MaskKey("test", "123")
		require.Error(t, err)
		assert.EqualError(t, err, fmt.Sprintf("mask value length is less than identifier suffix length (%d)", identifierValueSuffixLength))
	})

	t.Run("error: value length equals suffix length", func(t *testing.T) {
		t.Parallel()
		_, err := MaskKey("test", "1234")
		require.Error(t, err)
		assert.EqualError(t, err, fmt.Sprintf("mask value length is equal to identifier suffix length (%d), which would expose the entire identifier in the mask", identifierValueSuffixLength))
	})
}

func TestGenerateKey(t *testing.T) {
	t.Parallel()
	keyLength := 40

	t.Run("succeeds", func(t *testing.T) {
		t.Parallel()
		key, err := GenerateKey("test_")
		require.NoError(t, err)
		assert.Regexp(t, "^test_.*", key.PrefixedRawValue)
		assert.Equal(t, "test_", key.Masked.Prefix)
		assert.Equal(t, keyLength, key.Masked.ValueLength)
		assert.Regexp(t, "^[0-9a-f]{"+strconv.Itoa(identifierValuePrefixLength)+"}$", key.Masked.MaskedValuePrefix)
		assert.Regexp(t, "^[0-9a-f]{"+strconv.Itoa(identifierValueSuffixLength)+"}$", key.Masked.MaskedValueSuffix)
		assert.Regexp(t, "^\\$sha256\\$.*", key.HashedValue)
	})

	t.Run("no prefix", func(t *testing.T) {
		t.Parallel()
		key, err := GenerateKey("")
		require.NoError(t, err)
		assert.Regexp(t, "^[0-9a-f]{"+strconv.Itoa(keyLength)+"}$", key.PrefixedRawValue)
		assert.Empty(t, key.Masked.Prefix)
		assert.Equal(t, keyLength, key.Masked.ValueLength)
		assert.Regexp(t, "^[0-9a-f]{"+strconv.Itoa(identifierValuePrefixLength)+"}$", key.Masked.MaskedValuePrefix)
		assert.Regexp(t, "^[0-9a-f]{"+strconv.Itoa(identifierValueSuffixLength)+"}$", key.Masked.MaskedValueSuffix)
		assert.Regexp(t, "^\\$sha256\\$.*", key.HashedValue)
	})
}

func TestGetMaskedIdentifierProperties(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			result, err := MaskKey(tc.prefix, tc.value)

			if tc.expectedErrString != "" {
				require.EqualError(t, err, tc.expectedErrString)
				assert.Equal(t, tc.expectedResult, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedResult, result)
			}
		})
	}
}
