package db

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Janitor periodically removes expired auth and invite records. It is best
// effort: cleanup errors are logged and retried on the next pass.
type Janitor struct {
	db       *sql.DB
	interval time.Duration
	log      *slog.Logger
}

func NewJanitor(database *sql.DB, interval time.Duration, log *slog.Logger) *Janitor {
	if interval <= 0 {
		interval = time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Janitor{db: database, interval: interval, log: log}
}

func (j *Janitor) Start(ctx context.Context) {
	go func() {
		j.runOnce(ctx)
		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				j.runOnce(ctx)
			}
		}
	}()
}

func (j *Janitor) runOnce(ctx context.Context) {
	passCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tokens, tokenErr := j.deleteExpired(passCtx, "DELETE FROM user_tokens WHERE expires_at < CURRENT_TIMESTAMP")
	invites, inviteErr := j.deleteExpired(passCtx, "DELETE FROM invite_links WHERE expires_at < CURRENT_TIMESTAMP AND consumed_at IS NULL")
	if tokenErr != nil {
		j.log.Warn("janitor: expired user token cleanup failed", "err", tokenErr)
	}
	if inviteErr != nil {
		j.log.Warn("janitor: expired invite cleanup failed", "err", inviteErr)
	}
	if tokenErr == nil && inviteErr == nil {
		j.log.Debug("janitor: expired auth records cleaned", "user_tokens", tokens, "invite_links", invites)
	}
}

func (j *Janitor) deleteExpired(ctx context.Context, query string) (int64, error) {
	res, err := j.db.ExecContext(ctx, query)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
