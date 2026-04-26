package watchdog

import (
	"context"
	"log/slog"
	"time"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
)

// pythonPublisher is the subset of pythonworker.Publisher used by the watchdog.
type pythonPublisher interface {
	Process(ctx context.Context, req pythonworker.ProcessRequest) error
}

// Start runs the watchdog loop until ctx is cancelled.
// It scans for document_tasks stuck in 'processing' longer than cfg.WatchdogThreshold,
// re-queues them via Redis/Celery (incrementing retry_count), and permanently fails
// tasks that have exceeded cfg.WatchdogMaxRetries.
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
	tasks, err := q.ListStaleTasks(ctx, cutoff)
	if err != nil {
		log.Error("watchdog: failed to list stale tasks", "err", err)
		return
	}

	if len(tasks) == 0 {
		return
	}

	log.Info("watchdog: found stale tasks", slog.Int("count", len(tasks)))

	for _, task := range tasks {
		processTask(ctx, q, pub, cfg, log, task)
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
) {
	taskLog := log.With(
		slog.String("task_id", task.ID.String()),
		slog.String("module", task.ModuleName),
		slog.Int("retry_count", int(task.RetryCount)),
		slog.Int("max_retries", cfg.WatchdogMaxRetries),
	)

	if int(task.RetryCount) >= cfg.WatchdogMaxRetries {
		rows, err := q.MarkStaleTaskFailed(ctx, task.ID)
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
	// WHERE status = 'processing' is the compare-and-swap guard that prevents
	// two concurrent watchdog runs from double-claiming the same task.
	rows, err := q.MarkStaleTaskPending(ctx, task.ID)
	if err != nil {
		taskLog.Error("watchdog: failed to claim stale task", "err", err)
		return
	}
	if rows == 0 {
		taskLog.Info("watchdog: task already claimed by another instance (pending)")
		return
	}

	triggerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pub.Process(triggerCtx, pythonworker.ProcessRequest{
		TaskID:      task.ID.String(),
		DocumentID:  task.DocumentID.String(),
		ModuleName:  task.ModuleName,
		StoragePath: task.StoragePath,
	}); err != nil {
		// Best-effort: task is already set to 'pending', so it will be
		// picked up again on the next watchdog tick or manual trigger.
		taskLog.Error("watchdog: failed to re-queue task via python worker", "err", err)
		return
	}

	taskLog.Info("watchdog: stale task re-queued",
		slog.Int("new_retry_count", int(task.RetryCount)+1),
	)
}
