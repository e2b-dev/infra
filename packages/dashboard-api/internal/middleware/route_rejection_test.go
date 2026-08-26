package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRejectRoutes(t *testing.T) {
	t.Parallel()

	shouldReject := true
	router := gin.New()
	router.Use(RejectRoutes(
		[]RouteTemplate{{Method: http.MethodPost, Path: "/deprecated"}},
		func(context.Context) bool { return shouldReject },
		RouteRejection{
			Reason:  "deprecated_operation",
			Status:  http.StatusGone,
			Message: "This operation is no longer available.",
		},
	))

	reachedHandler := false
	handler := func(c *gin.Context) {
		reachedHandler = true
		c.Status(http.StatusNoContent)
	}
	router.POST("/deprecated", handler)
	router.GET("/deprecated", handler)
	router.POST("/available", handler)

	serve := func(method string, path string) *httptest.ResponseRecorder {
		t.Helper()

		reachedHandler = false
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), method, path, nil))

		return response
	}

	response := serve(http.MethodPost, "/deprecated")
	assert.Equal(t, http.StatusGone, response.Code)
	assert.JSONEq(t, `{"code":410,"message":"This operation is no longer available."}`, response.Body.String())
	assert.False(t, reachedHandler)

	shouldReject = false
	response = serve(http.MethodPost, "/deprecated")
	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.True(t, reachedHandler)

	shouldReject = true
	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/deprecated"},
		{http.MethodPost, "/available"},
	} {
		response = serve(request.method, request.path)
		assert.Equal(t, http.StatusNoContent, response.Code)
		assert.True(t, reachedHandler)
	}
}
