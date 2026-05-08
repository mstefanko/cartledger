package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	TokenKindPasswordReset = "password_reset"
	sqliteDateTimeFormat   = "2006-01-02 15:04:05"
)

var ErrInvalidToken = errors.New("invalid or expired token")

// GenerateToken returns 32 random bytes encoded as hex for URL-safe delivery.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashToken hashes a plaintext token for storage. Never store reset/invite
// tokens as plaintext: links are bearer credentials.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// IssueToken creates a DB-backed user token and returns only the plaintext
// token for out-of-band delivery.
func IssueToken(db *sql.DB, userID, kind string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("ttl must be positive")
	}
	plaintext, err := GenerateToken()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().UTC().Add(ttl).Format(sqliteDateTimeFormat)
	var id string
	err = db.QueryRow(
		"INSERT INTO user_tokens (user_id, kind, token_hash, expires_at) VALUES (?, ?, ?, ?) RETURNING id",
		userID, kind, HashToken(plaintext), expiresAt,
	).Scan(&id)
	if err != nil {
		return "", err
	}
	slog.Info("auth token issued", "token_id", id, "kind", kind)
	return plaintext, nil
}

// ConsumeToken atomically consumes a single-use token. Only one concurrent
// caller can observe RowsAffected()==1 for the DELETE inside the transaction.
func ConsumeToken(db *sql.DB, kind, plaintext string) (string, error) {
	tokenHash := HashToken(plaintext)
	now := time.Now().UTC().Format(sqliteDateTimeFormat)

	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var userID string
	err = tx.QueryRow(
		"SELECT user_id FROM user_tokens WHERE token_hash = ? AND kind = ? AND expires_at > ?",
		tokenHash, kind, now,
	).Scan(&userID)
	if err == sql.ErrNoRows {
		return "", ErrInvalidToken
	}
	if err != nil {
		return "", err
	}

	res, err := tx.Exec(
		"DELETE FROM user_tokens WHERE token_hash = ? AND kind = ? AND expires_at > ?",
		tokenHash, kind, now,
	)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrInvalidToken
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return userID, nil
}

// ValidateNewPassword is the shared password policy for setup, join, reset,
// change-password, and operator CLI flows.
func ValidateNewPassword(plaintext string) error {
	if len(plaintext) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	return nil
}

// SQLiteDateTime stores app-managed timestamps in the same sortable UTC shape
// used by migration CURRENT_TIMESTAMP defaults.
func SQLiteDateTime(t time.Time) string {
	return t.UTC().Format(sqliteDateTimeFormat)
}
