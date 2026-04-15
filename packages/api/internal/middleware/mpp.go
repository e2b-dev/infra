package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tempoxyz/mpp-go/mpp"
	mppserver "github.com/tempoxyz/mpp-go/server"
)

// MPPConfig holds configuration for the MPP payment middleware.
type MPPConfig struct {
	Enabled   bool
	SecretKey string
	Realm     string
	Currency  string
	Recipient string
}

// MPPMiddleware returns a Gin middleware that handles the MPP 402 payment flow.
//
// When MPP is enabled and a request arrives without standard E2B auth (API key
// or bearer token), the middleware checks for an Authorization: Payment header.
// If absent, it issues a 402 challenge. If present and valid, it verifies the
// payment credential and lets the request through with a Payment-Receipt header.
func MPPMiddleware(cfg MPPConfig, method mppserver.Method) gin.HandlerFunc {
	handler := mppserver.New(method, cfg.Realm, cfg.SecretKey)

	return func(c *gin.Context) {
		if !cfg.Enabled {
			c.Next()
			return
		}

		// Skip if the request already has standard E2B auth.
		if c.GetHeader("X-API-Key") != "" || hasBearer(c.GetHeader("Authorization")) {
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")

		// Only intercept if there's a Payment credential or no auth at all.
		if authHeader != "" && !hasPaymentScheme(authHeader) {
			c.Next()
			return
		}

		amount := chargeAmount(c.Request.Method, c.FullPath())
		if amount == "" {
			c.Next()
			return
		}

		// Free endpoints — let through without payment.
		if amount == "0" {
			c.Next()
			return
		}

		result, err := handler.Charge(c.Request.Context(), mppserver.ChargeParams{
			Authorization: authHeader,
			Amount:        amount,
			Currency:      cfg.Currency,
			Recipient:     cfg.Recipient,
		})
		if err != nil {
			writeMPPError(c, err)
			c.Abort()
			return
		}

		if result.IsChallenge() {
			c.Header("WWW-Authenticate", result.Challenge.ToWWWAuthenticate(cfg.Realm))
			c.Header("Content-Type", "application/problem+json")
			c.Header("Cache-Control", "no-store")
			c.Writer.WriteHeader(http.StatusPaymentRequired)
			pe := mpp.ErrPaymentRequired(cfg.Realm, "")
			json.NewEncoder(c.Writer).Encode(pe.ProblemDetails(result.Challenge.ID))
			c.Abort()
			return
		}

		// Payment verified.
		c.Header("Payment-Receipt", result.Receipt.ToPaymentReceipt())
		if result.Credential != nil && result.Credential.Source != "" {
			c.Set("mpp_payer", result.Credential.Source)
		}

		c.Next()
	}
}

func hasBearer(header string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(header)), "bearer ")
}

func hasPaymentScheme(header string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(header)), "payment ")
}

// chargeAmount returns the charge amount in base units for a given endpoint.
// Returns empty string for non-billable endpoints, "0" for free endpoints.
func chargeAmount(method, path string) string {
	switch {
	case method == "POST" && path == "/sandboxes":
		return "1000000" // 1 USDC
	case method == "POST" && strings.HasSuffix(path, "/connect"):
		return "500000" // 0.5 USDC
	case method == "POST" && strings.HasSuffix(path, "/timeout"):
		return "100000" // 0.1 USDC
	case method == "POST" && strings.HasSuffix(path, "/refreshes"):
		return "100000" // 0.1 USDC
	case method == "DELETE" && strings.Contains(path, "/sandboxes/"):
		return "0" // free
	default:
		return ""
	}
}

func writeMPPError(c *gin.Context, err error) {
	c.Header("Content-Type", "application/problem+json")
	c.Header("Cache-Control", "no-store")

	if pe, ok := err.(*mpp.PaymentError); ok {
		c.Writer.WriteHeader(pe.Status)
		json.NewEncoder(c.Writer).Encode(pe.ProblemDetails(""))
		return
	}

	c.Writer.WriteHeader(http.StatusPaymentRequired)
	problem := mpp.ErrVerificationFailed(err.Error())
	json.NewEncoder(c.Writer).Encode(problem.ProblemDetails(""))
}
