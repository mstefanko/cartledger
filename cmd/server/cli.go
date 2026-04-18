package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Build-time variables injected by goreleaser (.goreleaser.yml → ldflags).
// These are referenced from both `cartledger version` and the backup
// MANIFEST so an operator can always trace a backup back to a release.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// newRootCmd builds the cobra command tree. It is its own function (rather
// than a package-level var) so tests can instantiate a fresh tree per case.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "cartledger",
		Short: "Self-hosted grocery receipt tracker",
		Long: `cartledger is a self-hosted grocery receipt tracker that uses an LLM
to extract line items from receipt images.

With no subcommand, it runs the HTTP server (same as "cartledger serve").`,
		SilenceUsage: true,
		// When invoked with no args, default to `serve` so systemd units /
		// Dockerfiles that just exec "cartledger" keep working.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, args)
		},
	}

	root.AddCommand(newServeCmd())
	root.AddCommand(newBackupCmd())
	root.AddCommand(newRestoreCmd())
	root.AddCommand(newVersionCmd())

	return root
}

// Execute runs the root command and handles exit codes. Called from main.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra prints the error already (we set SilenceUsage=true so it
		// doesn't also print the usage block on every failure). Exit 1
		// without re-printing.
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server (default when no subcommand is given)",
		RunE:  runServe,
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the cartledger version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("cartledger %s (commit %s, built %s)\n", version, commit, date)
		},
	}
}
