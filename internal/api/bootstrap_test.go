package api

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mstefanko/cartledger/internal/db"
)

func newBootstrapTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Fatalf("database.Close: %v", err)
		}
	})
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return database
}

func TestLoadOrGenerateBootstrapTokenRefreshesConsumedTokenWhenNoUsers(t *testing.T) {
	database := newBootstrapTestDB(t)

	bootstrap, err := LoadOrGenerateBootstrapToken(database)
	if err != nil {
		t.Fatalf("LoadOrGenerateBootstrapToken: %v", err)
	}
	firstToken := bootstrap.Token()
	if firstToken == "" {
		t.Fatalf("expected first bootstrap token")
	}
	if err := bootstrap.MarkConsumed(database); err != nil {
		t.Fatalf("MarkConsumed: %v", err)
	}

	nextBootstrap, err := LoadOrGenerateBootstrapToken(database)
	if err != nil {
		t.Fatalf("LoadOrGenerateBootstrapToken after consume: %v", err)
	}
	nextToken := nextBootstrap.Token()
	if nextToken == "" {
		t.Fatalf("expected regenerated bootstrap token")
	}
	if nextToken == firstToken {
		t.Fatalf("expected regenerated token to differ from consumed token")
	}
}

func TestEnsureForEmptyUsersActivatesAfterUsersAreCleared(t *testing.T) {
	database := newBootstrapTestDB(t)
	if _, err := database.Exec(
		"INSERT INTO households (id, name) VALUES ('hh1', 'Household')",
	); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash) VALUES ('u1', 'hh1', 'owner@example.com', 'Owner', 'hash')",
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	bootstrap, err := LoadOrGenerateBootstrapToken(database)
	if err != nil {
		t.Fatalf("LoadOrGenerateBootstrapToken: %v", err)
	}
	if bootstrap.HasToken() {
		t.Fatalf("expected no bootstrap token while users exist")
	}

	if _, err := database.Exec("DELETE FROM users"); err != nil {
		t.Fatalf("delete users: %v", err)
	}
	token, activated, err := bootstrap.EnsureForEmptyUsers(database)
	if err != nil {
		t.Fatalf("EnsureForEmptyUsers: %v", err)
	}
	if !activated {
		t.Fatalf("expected bootstrap token to activate after users were cleared")
	}
	if token == "" || !bootstrap.HasToken() {
		t.Fatalf("expected active bootstrap token after users were cleared")
	}
}
