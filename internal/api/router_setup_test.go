package api

import "testing"

func TestShouldRedirectSPAToSetupWhenUsersEmpty(t *testing.T) {
	database := newBootstrapTestDB(t)

	redirect, err := shouldRedirectSPAToSetup(database, "login")
	if err != nil {
		t.Fatalf("shouldRedirectSPAToSetup: %v", err)
	}
	if !redirect {
		t.Fatalf("expected /login to redirect to setup when users table is empty")
	}

	redirect, err = shouldRedirectSPAToSetup(database, "setup")
	if err != nil {
		t.Fatalf("shouldRedirectSPAToSetup setup: %v", err)
	}
	if redirect {
		t.Fatalf("expected /setup not to redirect")
	}
}

func TestShouldRedirectSPAToSetupSkipsWhenUsersExist(t *testing.T) {
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

	redirect, err := shouldRedirectSPAToSetup(database, "login")
	if err != nil {
		t.Fatalf("shouldRedirectSPAToSetup: %v", err)
	}
	if redirect {
		t.Fatalf("expected /login not to redirect when users exist")
	}
}
