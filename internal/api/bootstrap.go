package api

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/mstefanko/cartledger/internal/config"
)

// Bootstrap holds the first-run setup token state. A single instance is
// created at server start when the users table is empty; the same instance is
// injected into the AuthHandler so /api/v1/setup can validate the token.
//
// Token lifecycle:
//
//  1. Boot: if users table is empty, LoadOrGenerateBootstrapToken either loads
//     an existing token row from `bootstrap_token` (restart case — operator
//     may have already copied the URL) or generates a fresh one and inserts.
//     If a previous token was consumed but the users table is empty again
//     (for example after auth data was cleared), a new token replaces it.
//  2. Validate: the Setup handler accepts the browser setup form without a
//     token while users is empty. If a token is supplied, it must match.
//  3. Consume: on successful setup, MarkConsumed() clears the in-memory
//     token and marks the DB row consumed. Any further /setup request is
//     rejected because users > 0 (and the in-memory token is gone).
type Bootstrap struct {
	mu    sync.RWMutex
	token string // empty after consumption or when users already existed at boot
}

// Token returns the current in-memory bootstrap token. Empty string means
// setup has already completed (or the server found users at boot). Safe for
// concurrent access.
func (b *Bootstrap) Token() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.token
}

// HasToken reports whether a usable bootstrap token is currently held.
func (b *Bootstrap) HasToken() bool {
	return b.Token() != ""
}

// Check returns true iff the provided candidate equals the in-memory
// bootstrap token. Uses constant-time comparison to avoid leaking the token
// via timing. Returns false if no token is held.
func (b *Bootstrap) Check(candidate string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.token == "" || candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(b.token), []byte(candidate)) == 1
}

// MarkConsumed clears the in-memory token and marks the DB row consumed.
// Idempotent: calling it twice is a no-op after the first call.
func (b *Bootstrap) MarkConsumed(db *sql.DB) error {
	b.mu.Lock()
	b.token = ""
	b.mu.Unlock()

	// Best-effort DB mark; the in-memory clear above is what actually blocks
	// replays, but updating the row keeps the DB state honest for anyone
	// inspecting it.
	_, err := db.Exec(
		`UPDATE bootstrap_token SET consumed_at = CURRENT_TIMESTAMP WHERE id = 1 AND consumed_at IS NULL`,
	)
	if err != nil {
		return fmt.Errorf("mark bootstrap token consumed: %w", err)
	}
	return nil
}

// EnsureForEmptyUsers ensures a usable bootstrap token exists when the users
// table is empty. The returned bool is true when the in-memory token was
// activated by this call, which lets callers print the one-time setup URL once.
//
// If users exist, this is a no-op and returns an empty token.
func (b *Bootstrap) EnsureForEmptyUsers(db *sql.DB) (string, bool, error) {
	b.mu.RLock()
	if b.token != "" {
		token := b.token
		b.mu.RUnlock()
		return token, false, nil
	}
	b.mu.RUnlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.token != "" {
		return b.token, false, nil
	}

	var userCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return "", false, fmt.Errorf("count users: %w", err)
	}
	if userCount > 0 {
		return "", false, nil
	}

	// Reuse a pending token if one already exists. This preserves an already
	// copied URL across restarts and across repeated /status probes.
	var existing string
	err := db.QueryRow(
		`SELECT token FROM bootstrap_token WHERE id = 1 AND consumed_at IS NULL`,
	).Scan(&existing)
	switch {
	case err == nil:
		b.token = existing
		return existing, true, nil
	case err == sql.ErrNoRows:
		// fall through to generation
	default:
		return "", false, fmt.Errorf("load bootstrap token: %w", err)
	}

	tok, err := generateBootstrapToken()
	if err != nil {
		return "", false, err
	}

	// A consumed bootstrap row can exist even when users are empty again, e.g.
	// if the users table was cleared during self-hosted recovery. Reset that
	// single-row token so the owner setup flow can be reached again.
	if _, err := db.Exec(
		`INSERT INTO bootstrap_token (id, token, consumed_at, created_at)
		 VALUES (1, ?, NULL, CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET
		   token = excluded.token,
		   consumed_at = NULL,
		   created_at = CURRENT_TIMESTAMP
		 WHERE bootstrap_token.consumed_at IS NOT NULL`,
		tok,
	); err != nil {
		return "", false, fmt.Errorf("upsert bootstrap token: %w", err)
	}

	var stored string
	if err := db.QueryRow(
		`SELECT token FROM bootstrap_token WHERE id = 1 AND consumed_at IS NULL`,
	).Scan(&stored); err != nil {
		return "", false, fmt.Errorf("read-back bootstrap token: %w", err)
	}
	b.token = stored
	return stored, true, nil
}

// LoadOrGenerateBootstrapToken sets up the first-run token if the users table
// is empty. It either loads an existing un-consumed row from bootstrap_token
// (restart case) or generates a fresh 32-byte base64url token and INSERTs it.
//
// Returns a *Bootstrap that is either:
//   - usable (Token() != ""): users table is empty; caller MUST print the
//     setup URL banner.
//   - idle (Token() == ""): users already exist; bootstrap is a no-op and
//     the Setup handler will reject any /api/v1/setup calls on the "users
//     already exist" branch.
func LoadOrGenerateBootstrapToken(db *sql.DB) (*Bootstrap, error) {
	bootstrap := &Bootstrap{}
	if _, _, err := bootstrap.EnsureForEmptyUsers(db); err != nil {
		return nil, err
	}
	return bootstrap, nil
}

// generateBootstrapToken returns 32 bytes of crypto/rand entropy, base64url-
// encoded without padding. ~43 ASCII chars — URL-safe without escaping.
func generateBootstrapToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate bootstrap token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// PrintBootstrapBanner writes a visible banner to stderr (so it cuts through
// JSON logs) and also emits a structured slog.Info record so log aggregators
// capture the URL.
//
// The URL uses http://localhost:<port> regardless of reverse-proxy settings:
// the operator on first-boot typically hits the server directly, and a misled
// URL is worse than an accurate localhost one (which an operator can trivially
// s/localhost/theirhost/ on). We emit a note in the banner about overriding it
// if the server is remote.
func PrintBootstrapBanner(cfg *config.Config, token string) {
	url := fmt.Sprintf("http://localhost:%s/setup?bootstrap=%s", cfg.Port, token)

	// slog record so JSON aggregators pick it up.
	slog.Info(
		"first-run bootstrap: open the setup URL to create the first admin user",
		"url", url,
		"note", "if the server is on a different host, swap localhost for its address",
	)

	// Visible stderr banner — plain text so it stands out against JSON logs.
	var lines []string
	lines = append(lines,
		"",
		"==============================================================================",
		"  CartLedger: first-run setup",
		"==============================================================================",
		"  No users exist yet. Open this URL in your browser to create the first admin:",
		"",
		"  "+url,
		"",
		"  If the server is not on localhost, replace 'localhost' with its address/hostname.",
		"  This URL is one-time: it stops working as soon as setup completes.",
	)
	if isProductionEnv() {
		lines = append(lines,
			"",
			"  PRODUCTION MODE DETECTED.",
			"  - Do NOT share this URL outside the operator.",
			"  - Ensure JWT_SECRET is set (not 'change-me-in-production').",
			"  - Prefer HTTPS in front of this server.",
		)
	}
	lines = append(lines, "==============================================================================", "")

	// Use fmt.Fprintln one line at a time so the message survives log
	// aggregators that split on newlines, but keeps the banner visually intact
	// at an attached terminal.
	_, _ = fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
}

// isProductionEnv duplicates config.isProduction (which is unexported). We
// intentionally do not export that helper from the config package for one
// caller — the same two env vars are read here.
func isProductionEnv() bool {
	if strings.EqualFold(os.Getenv("CARTLEDGER_ENV"), "production") {
		return true
	}
	switch strings.ToLower(os.Getenv("PROD")) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
