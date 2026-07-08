package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetWellKnownJWKS serves the public JSON Web Key Set for the volume content
// token signing key at /.well-known/jwks.json, allowing token verifiers to
// discover the verification keys (and rotations) dynamically.
//
// It is registered as a public, unauthenticated route (see NewGinServer) and
// serves a body precomputed at startup (a.volumesJWKS). It returns 404 when
// there is nothing to publish — signing disabled, or a symmetric (HMAC) key
// whose secret must never be published.
func (a *APIStore) GetWellKnownJWKS(c *gin.Context) {
	if a.volumesJWKS == nil {
		c.Status(http.StatusNotFound)

		return
	}

	c.Header("Cache-Control", "public, max-age=3600")
	c.Data(http.StatusOK, "application/json; charset=utf-8", a.volumesJWKS)
}
