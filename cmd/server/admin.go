package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

// newPromoteAdminCmd builds `cartledger promote-admin <email>`.
//
// Used by an operator who created the first user before the admin flag
// existed, or who wants to add a second admin. The command opens the DB
// at DATA_DIR (no HTTP server running required) and flips is_admin=1 on
// the matching row.
func newPromoteAdminCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote-admin <email>",
		Short: "Grant admin privileges to an existing user by email",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPromoteAdmin(args[0])
		},
	}
}

func runPromoteAdmin(email string) error {
	initLogger()
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("email is required")
	}

	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// Migrations must be current before the UPDATE — an operator running
	// promote-admin against a pre-019 DB would otherwise hit a missing-column
	// error that's confusing. Running migrations here is the same pattern the
	// server uses at boot.
	if err := db.RunMigrations(database); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	res, err := database.Exec("UPDATE users SET is_admin = 1 WHERE email = ?", email)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("no user with email %s", email)
	}

	fmt.Printf("promoted %s to admin\n", email)
	return nil
}
