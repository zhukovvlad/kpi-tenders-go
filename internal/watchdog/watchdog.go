package watchdog

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
)

// pythonPublisher is the subset of pythonworker.Publisher used by the watchdog.
type pythonPublisher interface {
	Process(ctx context.Context, req pythonworker.ProcessRequest) error
}

// Start runs the watchdog loop until ctx is cancelled.
// It scans for document_tasks stuck in 'pending' or 'processing' longer than
// cfg.WatchdogThreshold, re-queues them via Redis/Celery (incrementing
// retry_count), and permanently fails tasks that have exceeded cfg.WatchdogMaxRetries.
//
// The function is blocking; run it in a goroutine.
func Start(
	ctx context.Context,
	q repository.Querier,
	pub pythonPublisher,
	cfg *config.Config,
	log *slog.Logger,
) {
	log.Info("watchdog: started",
		slog.Duration("interval", cfg.WatchdogInterval),
		slog.Duration("threshold", cfg.WatchdogThreshold),
		slog.Int("max_retries", cfg.WatchdogMaxRetries),
	)

	ticker := time.NewTicker(cfg.WatchdogInterval)
	defer ticker.Stop()

	// Run immediately on startup, then on every tick.
	runOnce(ctx, q, pub, cfg, log)

	for {
		select {
		case <-ctx.Done():
			log.Info("watchdog: stopped")
			return
		case <-ticker.C:
			runOnce(ctx, q, pub, cfg, log)
		}
	}
}

// runOnce performs a single watchdog scan cycle.
func runOnce(
	ctx context.Context,
	q repository.Querier,
	pub pythonPublisher,
	cfg *config.Config,
	log *slog.Logger,
) {
	cutoff := time.Now().Add(-cfg.WatchdogThreshold)
	tasks, err := q.ListStaleTasks(ctx, repository.ListStaleTasksParams{
		UpdatedAt: cutoff,
		Limit:     int32(cfg.WatchdogBatchSize),
	})
	if err != nil {
		log.Error("watchdog: failed to list stale tasks", "err", err)
		return
	}

	if len(tasks) == 0 {
		return
	}

	log.Info("watchdog: found stale tasks", slog.Int("count", len(tasks)))

	for _, task := range tasks {
		processTask(ctx, q, pub, cfg, log, task, cutoff)
	}
}

// processTask handles a single stale task: fails it if retries exhausted, or re-queues it.
func processTask(
	ctx context.Context,
	q repository.Querier,
	pub pythonPublisher,
	cfg *config.Config,
	log *slog.Logger,
	task repository.ListStaleTasksRow,
	cutoff time.Time,
) {
	taskLog := log.With(
		slog.String("task_id", task.ID.String()),
		slog.String("module", task.ModuleName),
		slog.Int("retry_count", int(task.RetryCount)),
		slog.Int("max_retries", cfg.WatchdogMaxRetries),
	)

	if int(task.RetryCount) >= cfg.WatchdogMaxRetries {
		rows, err := q.MarkStaleTaskFailed(ctx, repository.MarkStaleTaskFailedParams{
			ID:           task.ID,
			UpdatedAt:    cutoff,
			ErrorMessage: pgtype.Text{String: fmt.Sprintf("stale task: exceeded max retry attempts (%d/%d)", task.RetryCount, cfg.WatchdogMaxRetries), Valid: true},
		})
		if err != nil {
			taskLog.Error("watchdog: failed to mark task as failed", "err", err)
			return
		}
		if rows == 0 {
			// Another watchdog instance claimed it first — safe to ignore.
			taskLog.Info("watchdog: task already claimed by another instance (fail)")
			return
		}
		taskLog.Warn("watchdog: task permanently failed (max retries exceeded)")
		return
	}

	// Atomically flip status → 'pending' and increment retry_count.
	// WHERE status IN ('pending', 'processing') AND updated_at < cutoff is the
	// compare-and-swap guard that prevents two concurrent watchdog runs from
	// double-claiming the same task, and avoids resetting a task refreshed
	// between ListStaleTasks and this UPDATE.
	rows, err := q.MarkStaleTaskPending(ctx, repository.MarkStaleTaskPendingParams{
		ID:        task.ID,
		UpdatedAt: cutoff,
	})
	if err != nil {
		taskLog.Error("watchdog: failed to claim stale task", "err", err)
		return
	}
	if rows == 0 {
		taskLog.Info("watchdog: task already claimed by another instance (pending)")
		return
	}

	req := pythonworker.ProcessRequest{
		TaskID:      task.ID.String(),
		DocumentID:  task.DocumentID.String(),
		ModuleName:  task.ModuleName,
		StoragePath: task.InputStoragePath,
	}

	const maxPublishAttempts = 3
	const publishAttemptTimeout = 5 * time.Second
	const publishRetryDelay = 500 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxPublishAttempts; attempt++ {
		triggerCtx, cancel := context.WithTimeout(ctx, publishAttemptTimeout)
		lastErr = pub.Process(triggerCtx, req)
		cancel()

		if lastErr == nil {
			taskLog.Info("watchdog: stale task re-queued",
				slog.Int("new_retry_count", int(task.RetryCount)+1),
				slog.Int("publish_attempt", attempt),
			)
			return
		}

		if attempt < maxPublishAttempts {
			taskLog.Warn("watchdog: failed to re-queue task via python worker; retrying",
				"err", lastErr,
				slog.Int("publish_attempt", attempt),
				slog.Int("max_publish_attempts", maxPublishAttempts),
			)
			select {
			case <-ctx.Done():
				taskLog.Error("watchdog: context canceled while retrying task re-queue", "err", ctx.Err())
				return
			case <-time.After(publishRetryDelay):
			}
		}
	}

	// Best-effort: task is already set to 'pending', so it will be picked up
	// again on the next watchdog tick (after WatchdogThreshold elapses).
	taskLog.Error("watchdog: failed to re-queue task via python worker after retries",
		"err", lastErr,
		slog.Int("max_publish_attempts", maxPublishAttempts),
	)
}
