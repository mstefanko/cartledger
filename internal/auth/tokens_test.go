package auth

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mstefanko/cartledger/internal/db"
)

func newTokenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	if _, err := database.Exec("INSERT INTO households (id, name) VALUES ('hh', 'Household')"); err != nil {
		t.Fatalf("seed household: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash) VALUES ('user', 'hh', 'u@example.com', 'User', 'hash')",
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestIssueConsumeToken(t *testing.T) {
	database := newTokenTestDB(t)

	token, err := IssueToken(database, "user", TokenKindPasswordReset, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if token == "" {
		t.Fatalf("IssueToken returned empty plaintext")
	}

	userID, err := ConsumeToken(database, TokenKindPasswordReset, token)
	if err != nil {
		t.Fatalf("ConsumeToken: %v", err)
	}
	if userID != "user" {
		t.Fatalf("userID = %q, want user", userID)
	}

	if _, err := ConsumeToken(database, TokenKindPasswordReset, token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second ConsumeToken err = %v, want ErrInvalidToken", err)
	}
}

func TestConsumeTokenRejectsExpired(t *testing.T) {
	database := newTokenTestDB(t)
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO user_tokens (user_id, kind, token_hash, expires_at) VALUES ('user', ?, ?, ?)",
		TokenKindPasswordReset, HashToken(token), SQLiteDateTime(time.Now().Add(-time.Hour)),
	); err != nil {
		t.Fatalf("insert expired token: %v", err)
	}

	if _, err := ConsumeToken(database, TokenKindPasswordReset, token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("ConsumeToken err = %v, want ErrInvalidToken", err)
	}
}

func TestConsumeTokenRejectsWrongKind(t *testing.T) {
	database := newTokenTestDB(t)
	token, err := IssueToken(database, "user", TokenKindPasswordReset, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	if _, err := ConsumeToken(database, "email_confirm", token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("ConsumeToken err = %v, want ErrInvalidToken", err)
	}
}
