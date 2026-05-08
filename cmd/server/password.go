package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/db"
)

func newResetPasswordCmd() *cobra.Command {
	var password string
	cmd := &cobra.Command{
		Use:   "reset-password <email>",
		Short: "Reset a user's password by email",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResetPassword(args[0], password)
		},
	}
	cmd.Flags().StringVar(&password, "password", "", "new password (for scripting; otherwise prompted)")
	return cmd
}

func runResetPassword(email, password string) error {
	initLogger()
	cfg, err := config.LoadBase()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("email is required")
	}
	if password == "" {
		password, err = promptNewPassword()
		if err != nil {
			return err
		}
	}
	if err := auth.ValidateNewPassword(password); err != nil {
		return err
	}

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	database, err := db.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()
	if err := db.RunMigrations(database); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	res, err := database.Exec(
		"UPDATE users SET password_hash = ?, password_changed_at = ? WHERE email = ?",
		passwordHash, auth.SQLiteDateTime(time.Now()), email,
	)
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

	fmt.Printf("reset password for %s\n", email)
	return nil
}

func promptNewPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("--password is required when stdin is not a terminal")
	}
	fmt.Fprint(os.Stderr, "New password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	fmt.Fprint(os.Stderr, "Confirm password: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password confirmation: %w", err)
	}
	if string(first) != string(second) {
		return "", fmt.Errorf("passwords do not match")
	}
	return string(first), nil
}
