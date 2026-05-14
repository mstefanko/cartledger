package runner

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrQueueFull          = errors.New("product enrichment queue is full")
	ErrWorkerShuttingDown = errors.New("product enrichment worker is shutting down")
	ErrJobAlreadyQueued   = errors.New("product enrichment job already queued")
)

type Worker struct {
	service *Service
	jobs    chan string
	log     *slog.Logger
	runCtx  context.Context
	cancel  context.CancelFunc

	wg          sync.WaitGroup
	mu          sync.Mutex
	queued      sync.Map
	accepting   bool
	shutdown    atomic.Bool
	shutdownRes chan struct{}
	stopPoll    chan struct{}
}

func NewWorker(concurrency int, service *Service) *Worker {
	if concurrency <= 0 {
		concurrency = 1
	}
	runCtx, cancel := context.WithCancel(context.Background())
	w := &Worker{
		service:     service,
		jobs:        make(chan string, 100),
		log:         slog.Default(),
		runCtx:      runCtx,
		cancel:      cancel,
		accepting:   true,
		shutdownRes: make(chan struct{}),
		stopPoll:    make(chan struct{}),
	}
	if service != nil {
		service.SetWorker(w)
	}
	for i := 0; i < concurrency; i++ {
		go w.process()
	}
	go w.pollReady()
	return w
}

func (w *Worker) QueueDepth() int {
	return len(w.jobs)
}

func (w *Worker) Submit(jobID string) error {
	if jobID == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.accepting {
		return ErrWorkerShuttingDown
	}
	if _, loaded := w.queued.LoadOrStore(jobID, struct{}{}); loaded {
		return ErrJobAlreadyQueued
	}
	select {
	case w.jobs <- jobID:
		w.wg.Add(1)
		return nil
	default:
		w.queued.Delete(jobID)
		return ErrQueueFull
	}
}

func (w *Worker) RequeueReady(ctx context.Context) (int, error) {
	rows, err := w.service.DB.QueryContext(ctx,
		`SELECT id
		   FROM product_enrichment_jobs
		  WHERE status = 'queued'
		    AND (next_attempt_at IS NULL OR datetime(next_attempt_at) <= datetime('now'))
		  ORDER BY
		    CASE trigger
		      WHEN 'manual_lookup' THEN 0
		      WHEN 'manual_refresh' THEN 0
		      WHEN 'receipt_review_scan' THEN 0
		      WHEN 'receipt_scan' THEN 1
		      WHEN 'batch_backfill' THEN 2
		      WHEN 'scheduled_refresh' THEN 3
		      ELSE 4
		    END,
		    COALESCE(next_attempt_at, queued_at),
		    queued_at
		  LIMIT 1000`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var submitted int
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return submitted, err
		}
		err := w.Submit(id)
		switch {
		case err == nil:
			submitted++
		case errors.Is(err, ErrJobAlreadyQueued):
			continue
		case errors.Is(err, ErrQueueFull), errors.Is(err, ErrWorkerShuttingDown):
			return submitted, nil
		default:
			return submitted, err
		}
	}
	return submitted, rows.Err()
}

func (w *Worker) RecoverStaleRunning(ctx context.Context, olderThan time.Duration, maxAttempts int) (int, error) {
	if olderThan <= 0 {
		olderThan = 30 * time.Minute
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	cutoff := time.Now().UTC().Add(-olderThan).Format("2006-01-02 15:04:05")
	res, err := w.service.DB.ExecContext(ctx,
		`UPDATE product_enrichment_jobs
		    SET status = 'queued',
		        started_at = NULL,
		        next_attempt_at = NULL,
		        last_error = 'recovered stale running job',
		        updated_at = CURRENT_TIMESTAMP
		  WHERE status = 'running'
		    AND datetime(updated_at) < datetime(?)
		    AND attempt_count < ?`,
		cutoff, maxAttempts,
	)
	if err != nil {
		return 0, err
	}
	recovered, _ := res.RowsAffected()
	_, err = w.service.DB.ExecContext(ctx,
		`UPDATE product_enrichment_jobs
		    SET status = 'failed',
		        finished_at = CURRENT_TIMESTAMP,
		        last_error = 'stale running job exceeded retry limit',
		        updated_at = CURRENT_TIMESTAMP
		  WHERE status = 'running'
		    AND datetime(updated_at) < datetime(?)
		    AND attempt_count >= ?`,
		cutoff, maxAttempts,
	)
	if err != nil {
		return int(recovered), err
	}
	return int(recovered), nil
}

func (w *Worker) Shutdown(ctx context.Context) error {
	if !w.shutdown.CompareAndSwap(false, true) {
		<-w.shutdownRes
		return nil
	}
	defer close(w.shutdownRes)
	defer w.cancel()

	close(w.stopPoll)
	w.mu.Lock()
	w.accepting = false
	close(w.jobs)
	w.mu.Unlock()

	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		w.cancel()
		for {
			select {
			case jobID, ok := <-w.jobs:
				if !ok {
					return ctx.Err()
				}
				w.queued.Delete(jobID)
				w.wg.Done()
			default:
				return ctx.Err()
			}
		}
	}
}

func (w *Worker) process() {
	for jobID := range w.jobs {
		if w.service != nil {
			if err := w.service.ProcessJob(w.runCtx, jobID); err != nil && w.log != nil {
				w.log.Warn("enrichment: job failed", "job_id", jobID, "err", err)
			}
		}
		w.queued.Delete(jobID)
		w.wg.Done()
	}
}

func (w *Worker) pollReady() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopPoll:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := w.RequeueReady(ctx)
			cancel()
			if err != nil && w.log != nil {
				w.log.Warn("enrichment: ready-job poll failed", "err", err)
			}
		}
	}
}
