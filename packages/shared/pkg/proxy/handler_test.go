package proxy

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStoreStatus_WriteHeader(t *testing.T) {
	r := httptest.NewRecorder()

	ww := &storeStatus{ResponseWriter: r}

	ww.WriteHeader(200)

	assert.Equal(t, 200, ww.statusCode)
}
