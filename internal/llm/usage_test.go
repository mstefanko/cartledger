package llm

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mstefanko/cartledger/internal/db"
)

// newUsageTestDB mirrors newSearchTestDB in internal/search. In-memory-ish
// SQLite via a tempdir, all migrations applied, one household seeded.
func newUsageTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage_test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var householdID string
	if err := database.QueryRow(
		"INSERT INTO households (name) VALUES ('UsageTest') RETURNING id",
	).Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	return database, householdID
}

func TestRecordAndGetMonthlyUsage(t *testing.T) {
	database, hh := newUsageTestDB(t)
	ym := "2026-04"

	row, err := GetMonthlyUsage(database, hh, ym)
	if err != nil {
		t.Fatalf("initial get: %v", err)
	}
	if row.InputTokens != 0 || row.OutputTokens != 0 || row.CallCount != 0 {
		t.Fatalf("initial row should be zero: %+v", row)
	}

	if err := RecordMonthlyUsage(database, hh, ym, 1000, 250); err != nil {
		t.Fatalf("record 1: %v", err)
	}
	if err := RecordMonthlyUsage(database, hh, ym, 500, 100); err != nil {
		t.Fatalf("record 2: %v", err)
	}

	row, err = GetMonthlyUsage(database, hh, ym)
	if err != nil {
		t.Fatalf("get after record: %v", err)
	}
	if row.InputTokens != 1500 || row.OutputTokens != 350 || row.CallCount != 2 {
		t.Fatalf("want (1500, 350, 2); got %+v", row)
	}
}

func TestCheckBudget(t *testing.T) {
	database, hh := newUsageTestDB(t)
	ym := CurrentYearMonth()

	// Budget=0 → never errors.
	if err := CheckBudget(database, hh, 0); err != nil {
		t.Fatalf("budget=0 should pass; got %v", err)
	}

	// Under budget.
	if err := RecordMonthlyUsage(database, hh, ym, 100, 100); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := CheckBudget(database, hh, 1000); err != nil {
		t.Fatalf("under budget should pass; got %v", err)
	}

	// Push over.
	if err := RecordMonthlyUsage(database, hh, ym, 500, 400); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := CheckBudget(database, hh, 1000); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("over budget should return ErrBudgetExceeded; got %v", err)
	}
}
