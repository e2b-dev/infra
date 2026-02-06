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

	t.Run("GZIP is supported (case-insensitive)", func(t *testing.T) {
		t.Parallel()
		assert.True(t, isSupportedEncoding("GZIP"))
	})

	t.Run("Gzip is supported (case-insensitive)", func(t *testing.T) {
		t.Parallel()
		assert.True(t, isSupportedEncoding("Gzip"))
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

func TestParseEncodingWithQuality(t *testing.T) {
	t.Parallel()

	t.Run("returns encoding with default quality 1.0", func(t *testing.T) {
		t.Parallel()
		eq := parseEncodingWithQuality("gzip")
		assert.Equal(t, "gzip", eq.encoding)
		assert.InDelta(t, 1.0, eq.quality, 0.001)
	})

	t.Run("parses quality value", func(t *testing.T) {
		t.Parallel()
		eq := parseEncodingWithQuality("gzip;q=0.5")
		assert.Equal(t, "gzip", eq.encoding)
		assert.InDelta(t, 0.5, eq.quality, 0.001)
	})

	t.Run("parses quality value with whitespace", func(t *testing.T) {
		t.Parallel()
		eq := parseEncodingWithQuality("gzip ; q=0.8")
		assert.Equal(t, "gzip", eq.encoding)
		assert.InDelta(t, 0.8, eq.quality, 0.001)
	})

	t.Run("handles q=0", func(t *testing.T) {
		t.Parallel()
		eq := parseEncodingWithQuality("gzip;q=0")
		assert.Equal(t, "gzip", eq.encoding)
		assert.InDelta(t, 0.0, eq.quality, 0.001)
	})

	t.Run("handles invalid quality value", func(t *testing.T) {
		t.Parallel()
		eq := parseEncodingWithQuality("gzip;q=invalid")
		assert.Equal(t, "gzip", eq.encoding)
		assert.InDelta(t, 1.0, eq.quality, 0.001) // defaults to 1.0 on parse error
	})

	t.Run("trims whitespace from encoding", func(t *testing.T) {
		t.Parallel()
		eq := parseEncodingWithQuality("  gzip  ")
		assert.Equal(t, "gzip", eq.encoding)
		assert.InDelta(t, 1.0, eq.quality, 0.001)
	})

	t.Run("normalizes encoding to lowercase", func(t *testing.T) {
		t.Parallel()
		eq := parseEncodingWithQuality("GZIP")
		assert.Equal(t, "gzip", eq.encoding)
	})

	t.Run("normalizes mixed case encoding", func(t *testing.T) {
		t.Parallel()
		eq := parseEncodingWithQuality("Gzip;q=0.5")
		assert.Equal(t, "gzip", eq.encoding)
		assert.InDelta(t, 0.5, eq.quality, 0.001)
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

	t.Run("returns identity when no header", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", nil)

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("returns gzip when Content-Encoding is gzip", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "gzip")

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns gzip when Content-Encoding is GZIP (case-insensitive)", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "GZIP")

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns gzip when Content-Encoding is Gzip (case-insensitive)", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "Gzip")

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns identity for identity encoding", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "identity")

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("returns error for unsupported encoding", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "br")

		_, err := parseContentEncoding(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported Content-Encoding")
		assert.Contains(t, err.Error(), "supported: [gzip]")
	})

	t.Run("handles gzip with quality value", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", nil)
		req.Header.Set("Content-Encoding", "gzip;q=1.0")

		encoding, err := parseContentEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})
}

func TestParseAcceptEncoding(t *testing.T) {
	t.Parallel()

	t.Run("returns identity when no header", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("returns gzip when Accept-Encoding is gzip", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns gzip when Accept-Encoding is GZIP (case-insensitive)", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "GZIP")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns gzip when gzip is among multiple encodings", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "deflate, gzip, br")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns gzip with quality value", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip;q=1.0")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns identity for identity encoding", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "identity")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("returns identity for wildcard encoding", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "*")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("falls back to identity for unsupported encoding only", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "br")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("falls back to identity when only unsupported encodings", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "deflate, br")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("selects gzip when it has highest quality", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "br;q=0.5, gzip;q=1.0, deflate;q=0.8")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("selects gzip even with lower quality when others unsupported", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "br;q=1.0, gzip;q=0.5")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding)
	})

	t.Run("returns identity when it has higher quality than gzip", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip;q=0.5, identity;q=1.0")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("skips encoding with q=0", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip;q=0, identity")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("falls back to identity when gzip rejected and no other supported", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip;q=0, br")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, EncodingIdentity, encoding)
	})

	t.Run("returns error when identity explicitly rejected and no supported encoding", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "br, identity;q=0")

		_, err := parseAcceptEncoding(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no acceptable encoding found")
	})

	t.Run("returns gzip for wildcard when identity rejected", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
		req.Header.Set("Accept-Encoding", "*, identity;q=0")

		encoding, err := parseAcceptEncoding(req)
		require.NoError(t, err)
		assert.Equal(t, "gzip", encoding) // wildcard with identity rejected returns supported encoding
	})
}

func TestGetDecompressedBody(t *testing.T) {
	t.Parallel()

	t.Run("returns original body when no Content-Encoding header", func(t *testing.T) {
		t.Parallel()
		content := []byte("test content")
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", bytes.NewReader(content))

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

		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", bytes.NewReader(compressed.Bytes()))
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
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", bytes.NewReader(invalidGzip))
		req.Header.Set("Content-Encoding", "gzip")

		_, err := getDecompressedBody(req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create gzip reader")
	})

	t.Run("returns original body for identity encoding", func(t *testing.T) {
		t.Parallel()
		content := []byte("test content")
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", bytes.NewReader(content))
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
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", bytes.NewReader(content))
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

		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/test", bytes.NewReader(compressed.Bytes()))
		req.Header.Set("Content-Encoding", "gzip;q=1.0")

		body, err := getDecompressedBody(req)
		require.NoError(t, err)
		defer body.Close()

		data, err := io.ReadAll(body)
		require.NoError(t, err)
		assert.Equal(t, originalContent, data)
	})
}
