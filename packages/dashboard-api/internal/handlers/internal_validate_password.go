package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type validatePasswordRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type validatePasswordResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

func (s *APIStore) HandleInternalValidatePassword(c *gin.Context) {
	if c.GetHeader("X-Admin-Token") != s.config.AdminToken {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}

	var req validatePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "email and password required"})
		return
	}

	userID, encryptedPassword, err := s.authDB.GetEncryptedPassword(c.Request.Context(), req.Email)
	if err != nil || encryptedPassword == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "invalid credentials"})
		return
	}

	if !checkPassword(req.Password, encryptedPassword) {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "invalid credentials"})
		return
	}

	c.JSON(http.StatusOK, validatePasswordResponse{UserID: userID, Email: req.Email})
}

// checkPassword verifies a plaintext password against a stored hash.
// Supports bcrypt ("$2..." prefix) and the custom "<salt>:<sha256hex(password+salt)>" format.
func checkPassword(password, stored string) bool {
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil
	}
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	salt, expectedHex := parts[0], parts[1]
	sum := sha256.Sum256([]byte(password + salt))
	return hex.EncodeToString(sum[:]) == expectedHex
}
