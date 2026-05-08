package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenType distinguishes auth tokens from other future token types.
type TokenType string

const (
	TokenTypeAuth TokenType = "auth"
)

// Claims are the JWT claims for auth tokens (login/setup/join).
type Claims struct {
	UserID      string    `json:"user_id"`
	HouseholdID string    `json:"household_id"`
	Type        TokenType `json:"type"`
	jwt.RegisteredClaims
}

// CreateAuthToken creates a signed JWT for an authenticated user (30-day expiry).
func CreateAuthToken(secret, userID, householdID string) (string, error) {
	claims := Claims{
		UserID:      userID,
		HouseholdID: householdID,
		Type:        TokenTypeAuth,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(30 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ValidateAuthToken parses and validates an auth JWT. Returns the claims on success.
func ValidateAuthToken(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	if claims.Type != TokenTypeAuth {
		return nil, fmt.Errorf("not an auth token")
	}
	return claims, nil
}
