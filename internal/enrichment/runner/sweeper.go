package runner

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type SweepResult struct {
	HouseholdsChecked int
	JobsQueued        int
	SnapshotsPruned   int
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || s.DB == nil {
		return
	}
	var interval time.Duration
	if s.Cfg != nil {
		interval = s.Cfg.ProductEnrichmentSweepInterval
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if s.Cfg != nil && s.Cfg.ProductEnrichmentScheduledSweep {
		go s.runSweepScheduler(ctx, interval)
	}
	go s.runPruneScheduler(ctx, interval)
}

func (s *Service) runSweepScheduler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCtx, cancel := context.WithTimeout(ctx, minDuration(interval/2, 30*time.Minute))
			result, err := s.SweepOnce(runCtx)
			cancel()
			if err != nil && s.Log != nil {
				s.Log.Warn("enrichment: scheduled sweep failed", "err", err)
				continue
			}
			if s.Log != nil && result.JobsQueued > 0 {
				s.Log.Info("enrichment: scheduled sweep finished", "households", result.HouseholdsChecked, "queued", result.JobsQueued)
			}
		}
	}
}

func (s *Service) runPruneScheduler(ctx context.Context, interval time.Duration) {
	prune := func() {
		runCtx, cancel := context.WithTimeout(ctx, minDuration(interval/2, 30*time.Minute))
		pruned, err := s.PruneSnapshots(runCtx, 1000)
		cancel()
		if err != nil && s.Log != nil {
			s.Log.Warn("enrichment: snapshot prune failed", "err", err)
			return
		}
		if s.Log != nil && pruned > 0 {
			s.Log.Info("enrichment: snapshot prune finished", "pruned", pruned)
		}
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				prune()
			}
		}
	}()
}

func (s *Service) SweepOnce(ctx context.Context) (SweepResult, error) {
	var result SweepResult
	if s == nil || s.DB == nil {
		return result, nil
	}
	if s.Cfg != nil && !s.Cfg.ProductEnrichmentScheduledSweep {
		return result, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT household_id, first_run_backfill_limit
		   FROM product_enrichment_settings
		  WHERE scheduled_sweep_enabled = 1`,
	)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var householdID string
		var firstRunLimit int
		if err := rows.Scan(&householdID, &firstRunLimit); err != nil {
			return result, err
		}
		result.HouseholdsChecked++
		queued, err := s.sweepHousehold(ctx, householdID, firstRunLimit)
		if err != nil {
			if s.Log != nil {
				s.Log.Warn("enrichment: household sweep failed", "household_id", householdID, "err", err)
			}
			continue
		}
		result.JobsQueued += queued
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) sweepHousehold(ctx context.Context, householdID string, firstRunLimit int) (int, error) {
	limit := 50
	if s.Cfg != nil && s.Cfg.ProductEnrichmentMaxJobsPerSweep > 0 {
		limit = s.Cfg.ProductEnrichmentMaxJobsPerSweep
	}
	if firstRunLimit > limit && !s.householdHasFinishedScheduledJob(ctx, householdID) {
		limit = firstRunLimit
	}
	refreshDays := 90
	if s.Cfg != nil && s.Cfg.ProductEnrichmentRefreshAfterDays > 0 {
		refreshDays = s.Cfg.ProductEnrichmentRefreshAfterDays
	}
	recentCutoff := time.Now().UTC().AddDate(0, 0, -refreshDays).Format("2006-01-02 15:04:05")
	rows, err := s.DB.QueryContext(ctx,
		`SELECT p.id, p.upc
		   FROM products p
		  WHERE p.household_id = ?
		    AND p.upc IS NOT NULL
		    AND TRIM(p.upc) != ''
		    AND NOT EXISTS (
		        SELECT 1
		          FROM product_enrichment_jobs active
		         WHERE active.household_id = p.household_id
		           AND active.product_id = p.id
		           AND active.trigger = 'scheduled_refresh'
		           AND active.status IN ('queued', 'running')
		    )
		    AND NOT EXISTS (
		        SELECT 1
		          FROM product_enrichment_jobs failed
		         WHERE failed.household_id = p.household_id
		           AND failed.product_id = p.id
		           AND failed.status = 'failed'
		           AND failed.next_attempt_at IS NOT NULL
		           AND datetime(failed.next_attempt_at) > datetime('now')
		    )
		    AND (
		        p.brand IS NULL OR TRIM(p.brand) = ''
		        OR p.pack_quantity IS NULL
		        OR p.pack_unit IS NULL OR TRIM(p.pack_unit) = ''
		        OR NOT EXISTS (
		            SELECT 1
		              FROM product_external_metadata pem
		             WHERE pem.household_id = p.household_id
		               AND pem.product_id = p.id
		               AND pem.last_error IS NULL
		               AND pem.fetched_at IS NOT NULL
		               AND datetime(pem.fetched_at) >= datetime(?)
		        )
		        OR EXISTS (
		            SELECT 1
		              FROM product_enrichment_jobs retry
		             WHERE retry.household_id = p.household_id
		               AND retry.product_id = p.id
		               AND retry.status = 'failed'
		               AND retry.next_attempt_at IS NOT NULL
		               AND datetime(retry.next_attempt_at) <= datetime('now')
		        )
		    )
		  ORDER BY (p.last_purchased_at IS NULL), p.last_purchased_at DESC, p.purchase_count DESC, p.updated_at DESC
		  LIMIT ?`,
		householdID, recentCutoff, limit,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	queued := 0
	for rows.Next() {
		var productID string
		var rawUPC sql.NullString
		if err := rows.Scan(&productID, &rawUPC); err != nil {
			return queued, err
		}
		if !rawUPC.Valid || strings.TrimSpace(rawUPC.String) == "" {
			continue
		}
		result, err := s.QueueForProduct(ctx, QueueForProductRequest{
			HouseholdID: householdID,
			ProductID:   productID,
			Trigger:     TriggerScheduled,
			UPC:         rawUPC.String,
		})
		if err != nil {
			if s.Log != nil {
				s.Log.Warn("enrichment: scheduled queue failed", "household_id", householdID, "product_id", productID, "err", err)
			}
			continue
		}
		if result.Queued {
			queued++
		}
	}
	return queued, rows.Err()
}

func (s *Service) householdHasFinishedScheduledJob(ctx context.Context, householdID string) bool {
	var exists int
	err := s.DB.QueryRowContext(ctx,
		`SELECT 1
		   FROM product_enrichment_jobs
		  WHERE household_id = ?
		    AND trigger IN ('scheduled_refresh', 'batch_backfill')
		    AND status IN ('succeeded', 'partial', 'failed', 'cancelled')
		  LIMIT 1`,
		householdID,
	).Scan(&exists)
	return err == nil
}

func (s *Service) PruneSnapshots(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 1000
	}
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM product_external_metadata
		  WHERE id IN (
		      SELECT pem.id
		        FROM product_external_metadata pem
		       WHERE pem.fetched_at IS NOT NULL
		         AND (
		             (pem.last_error IS NOT NULL AND datetime(pem.fetched_at) < datetime('now', '-30 days'))
		             OR
		             (pem.last_error IS NULL AND datetime(pem.fetched_at) < datetime('now', '-180 days'))
		         )
		         AND NOT EXISTS (
		             SELECT 1
		               FROM product_enrichment_suggestions pes
		              WHERE pes.external_metadata_id = pem.id
		                AND pes.status = 'accepted'
		         )
		       ORDER BY pem.fetched_at
		       LIMIT ?
		  )`,
		limit,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if a < b {
		return a
	}
	return b
}
