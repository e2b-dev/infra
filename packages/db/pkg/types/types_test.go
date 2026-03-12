package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONBStringMap_MarshalJSON_Nil(t *testing.T) {
	var m JSONBStringMap
	data, err := json.Marshal(m)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data))
}

func TestJSONBStringMap_MarshalJSON_Empty(t *testing.T) {
	m := JSONBStringMap{}
	data, err := json.Marshal(m)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data))
}

func TestJSONBStringMap_MarshalJSON_WithValues(t *testing.T) {
	m := JSONBStringMap{"key": "value"}
	data, err := json.Marshal(m)
	require.NoError(t, err)
	assert.Equal(t, `{"key":"value"}`, string(data))
}

func TestJSONBStringMap_UnmarshalJSON_Null(t *testing.T) {
	var m JSONBStringMap
	err := json.Unmarshal([]byte("null"), &m)
	require.NoError(t, err)
	assert.NotNil(t, m)
	assert.Empty(t, m)
}

func TestJSONBStringMap_UnmarshalJSON_EmptyObject(t *testing.T) {
	var m JSONBStringMap
	err := json.Unmarshal([]byte("{}"), &m)
	require.NoError(t, err)
	assert.NotNil(t, m)
	assert.Empty(t, m)
}

func TestJSONBStringMap_UnmarshalJSON_WithValues(t *testing.T) {
	var m JSONBStringMap
	err := json.Unmarshal([]byte(`{"key":"value"}`), &m)
	require.NoError(t, err)
	assert.Equal(t, JSONBStringMap{"key": "value"}, m)
}

func TestJSONBStringMap_RoundTrip(t *testing.T) {
	original := JSONBStringMap{"foo": "bar", "baz": "qux"}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded JSONBStringMap
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

func TestJSONBStringMap_NilRoundTrip(t *testing.T) {
	var original JSONBStringMap // nil
	data, err := json.Marshal(original)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data))

	var decoded JSONBStringMap
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.NotNil(t, decoded)
	assert.Empty(t, decoded)
}
