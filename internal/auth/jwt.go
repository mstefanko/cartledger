package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenType distinguishes auth tokens from invite tokens.
type TokenType string

const (
	TokenTypeAuth   TokenType = "auth"
	TokenTypeInvite TokenType = "invite"
)

// Claims are the JWT claims for auth tokens (login/setup/join).
type Claims struct {
	UserID      string    `json:"user_id"`
	HouseholdID string    `json:"household_id"`
	Type        TokenType `json:"type"`
	jwt.RegisteredClaims
}

// InviteClaims are the JWT claims for invite tokens.
type InviteClaims struct {
	HouseholdID string    `json:"household_id"`
	InviterID   string    `json:"inviter_id"`
	InviterName string    `json:"inviter_name"`
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

// CreateInviteToken creates a signed JWT for an invite link (7-day expiry).
func CreateInviteToken(secret, householdID, inviterID, inviterName string) (string, error) {
	claims := InviteClaims{
		HouseholdID: householdID,
		InviterID:   inviterID,
		InviterName: inviterName,
		Type:        TokenTypeInvite,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
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

// ValidateInviteToken parses and validates an invite JWT. Returns the claims on success.
func ValidateInviteToken(secret, tokenStr string) (*InviteClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &InviteClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*InviteClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	if claims.Type != TokenTypeInvite {
		return nil, fmt.Errorf("not an invite token")
	}
	return claims, nil
}
