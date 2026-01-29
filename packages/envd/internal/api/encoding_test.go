package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsSupportedEncoding(t *testing.T) {
	t.Parallel()

	t.Run("gzip is supported", func(t *testing.T) {
		t.Parallel()
		assert.True(t, isSupportedEncoding("gzip"))
	})

	t.Run("br is not supported", func(t *testing.T) {
		t.Parallel()
		assert.False(t, isSupportedEncoding("br"))
	})

	t.Run("deflate is not supported", func(t *testing.T) {
		t.Parallel()
		assert.False(t, isSupportedEncoding("deflate"))
	})
}

func TestParseEncoding(t *testing.T) {
	t.Parallel()

	t.Run("returns encoding as-is", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "gzip", parseEncoding("gzip"))
	})

	t.Run("trims whitespace", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "gzip", parseEncoding("  gzip  "))
	})

	t.Run("strips quality value", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "gzip", parseEncoding("gzip;q=1.0"))
	})

	t.Run("strips quality value with whitespace", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "gzip", parseEncoding("gzip ; q=0.5"))
	})
}

func TestParseContentEncoding(t *testing.T) {
	t.Parallel()

	t.Run("returns empty string when no header", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodPost, "/test", nil)

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "", encoding)
	})

	t.Run("returns gzip when Content-Encoding is gzip", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "gzip")

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns empty string for identity encoding", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "identity")

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "", encoding)
	})

	t.Run("returns error for unsupported encoding", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "br")

		_, err := parseContentEncoding(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported Content-Encoding")
		assert.Contains(t, err.Error(), "supported: [gzip]")
	})

	t.Run("handles gzip with quality value", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "gzip;q=1.0")

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})
}

func TestParseAcceptEncoding(t *testing.T) {
	t.Parallel()

	t.Run("returns empty string when no header", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "", encoding)
	})

	t.Run("returns gzip when Accept-Encoding is gzip", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns gzip when gzip is among multiple encodings", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "deflate, gzip, br")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns gzip with quality value", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip;q=1.0")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns empty string for identity encoding", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "identity")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "", encoding)
	})

	t.Run("returns empty string for wildcard encoding", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "*")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "", encoding)
	})

	t.Run("returns error for unsupported encoding only", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "br")

		_, err := parseAcceptEncoding(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported Accept-Encoding")
		assert.Contains(t, err.Error(), "supported: [gzip]")
	})

	t.Run("returns error when only unsupported encodings", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "deflate, br")

		_, err := parseAcceptEncoding(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported Accept-Encoding")
	})
}

func TestGetDecompressedBody(t *testing.T) {
	t.Parallel()

	t.Run("returns original body when no Content-Encoding header", func(t *testing.T) {
		t.Parallel()
		content := []byte("test content")
		req, _ := http.NewRequest(http.MethodPost, "/test", bytes.NewReader(content))

		body, err := getDecompressedBody(req)
		require.NoError(t, err)
		assert.Equal(t, req.Body, body, "should return original body")

		data, err := io.ReadAll(body)
		require.NoError(t, err)
		assert.Equal(t, content, data)
	})

	t.Run("decompresses gzip body when Content-Encoding is gzip", func(t *testing.T) {
		t.Parallel()
		originalContent := []byte("test content to compress")

		var compressed bytes.Buffer
		gw := gzip.NewWriter(&compressed)
		_, err := gw.Write(originalContent)
		require.NoError(t, err)
		err = gw.Close()
		require.NoError(t, err)

		req, _ := http.NewRequest(http.MethodPost, "/test", bytes.NewReader(compressed.Bytes()))
		req.Header.Set("Content-Encoding", "gzip")

		body, err := getDecompressedBody(req)
		require.NoError(t, err)
		defer body.Close()

		assert.NotEqual(t, req.Body, body, "should return a new gzip reader")

		data, err := io.ReadAll(body)
		require.NoError(t, err)
		assert.Equal(t, originalContent, data)
	})

	t.Run("returns error for invalid gzip data", func(t *testing.T) {
		t.Parallel()
		invalidGzip := []byte("this is not gzip data")
		req, _ := http.NewRequest(http.MethodPost, "/test", bytes.NewReader(invalidGzip))
		req.Header.Set("Content-Encoding", "gzip")

		_, err := getDecompressedBody(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create gzip reader")
	})

	t.Run("returns original body for identity encoding", func(t *testing.T) {
		t.Parallel()
		content := []byte("test content")
		req, _ := http.NewRequest(http.MethodPost, "/test", bytes.NewReader(content))
		req.Header.Set("Content-Encoding", "identity")

		body, err := getDecompressedBody(req)
		require.NoError(t, err)
		assert.Equal(t, req.Body, body, "should return original body")

		data, err := io.ReadAll(body)
		require.NoError(t, err)
		assert.Equal(t, content, data)
	})

	t.Run("returns error for unsupported encoding", func(t *testing.T) {
		t.Parallel()
		content := []byte("test content")
		req, _ := http.NewRequest(http.MethodPost, "/test", bytes.NewReader(content))
		req.Header.Set("Content-Encoding", "br")

		_, err := getDecompressedBody(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported Content-Encoding")
	})

	t.Run("handles gzip with quality value", func(t *testing.T) {
		t.Parallel()
		originalContent := []byte("test content to compress")

		var compressed bytes.Buffer
		gw := gzip.NewWriter(&compressed)
		_, err := gw.Write(originalContent)
		require.NoError(t, err)
		err = gw.Close()
		require.NoError(t, err)

		req, _ := http.NewRequest(http.MethodPost, "/test", bytes.NewReader(compressed.Bytes()))
		req.Header.Set("Content-Encoding", "gzip;q=1.0")

		body, err := getDecompressedBody(req)
		require.NoError(t, err)
		defer body.Close()

		data, err := io.ReadAll(body)
		require.NoError(t, err)
		assert.Equal(t, originalContent, data)
	})
}
