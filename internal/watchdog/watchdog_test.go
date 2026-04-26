package watchdog

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	storemock "go-kpi-tenders/internal/store/mock"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

func testCfg() *config.Config {
	return &config.Config{
		WatchdogInterval:   2 * time.Minute,
		WatchdogThreshold:  10 * time.Minute,
		WatchdogMaxRetries: 3,
		WatchdogBatchSize:  100,
	}
}

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

// mockPublisher is a testify mock for the pythonPublisher interface.
type mockPublisher struct {
	mock.Mock
}

func (m *mockPublisher) Process(ctx context.Context, req pythonworker.ProcessRequest) error {
	args := m.Called(ctx, req)
	return args.Error(0)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestRunOnce_RequeuesStaleTask verifies that a stale task with retry_count < max
// is claimed (MarkStaleTaskPending) and then re-queued via the python publisher.
func TestRunOnce_RequeuesStaleTask(t *testing.T) {
	mq := new(storemock.MockQuerier)
	pub := new(mockPublisher)
	cfg := testCfg()

	taskID := uuid.New()
	docID := uuid.New()
	staleTask := repository.ListStaleTasksRow{
		ID:               taskID,
		DocumentID:       docID,
		ModuleName:       "convert",
		RetryCount:       0,
		InputStoragePath: "bucket/docs/test.pdf",
	}

	mq.On("ListStaleTasks", mock.Anything, mock.MatchedBy(func(p repository.ListStaleTasksParams) bool {
		return p.Limit == int32(cfg.WatchdogBatchSize)
	})).
		Return([]repository.ListStaleTasksRow{staleTask}, nil)
	mq.On("MarkStaleTaskPending", mock.Anything, mock.MatchedBy(func(p repository.MarkStaleTaskPendingParams) bool {
		return p.ID == taskID
	})).Return(int64(1), nil)
	pub.On("Process", mock.Anything, pythonworker.ProcessRequest{
		TaskID:      taskID.String(),
		DocumentID:  docID.String(),
		ModuleName:  "convert",
		StoragePath: "bucket/docs/test.pdf",
	}).Return(nil)

	runOnce(context.Background(), mq, pub, cfg, testLog())

	mq.AssertExpectations(t)
	pub.AssertExpectations(t)
}

// TestRunOnce_FailsTaskWhenMaxRetriesExceeded verifies that a task that has
// reached cfg.WatchdogMaxRetries is permanently failed and NOT re-queued.
func TestRunOnce_FailsTaskWhenMaxRetriesExceeded(t *testing.T) {
	mq := new(storemock.MockQuerier)
	pub := new(mockPublisher)
	cfg := testCfg()

	taskID := uuid.New()
	staleTask := repository.ListStaleTasksRow{
		ID:         taskID,
		DocumentID: uuid.New(),
		ModuleName: "convert",
		RetryCount: int32(cfg.WatchdogMaxRetries), // exhausted
	}

	mq.On("ListStaleTasks", mock.Anything, mock.MatchedBy(func(p repository.ListStaleTasksParams) bool {
		return p.Limit == int32(cfg.WatchdogBatchSize)
	})).
		Return([]repository.ListStaleTasksRow{staleTask}, nil)
	mq.On("MarkStaleTaskFailed", mock.Anything, mock.MatchedBy(func(p repository.MarkStaleTaskFailedParams) bool {
		return p.ID == taskID && p.ErrorMessage.Valid
	})).Return(int64(1), nil)

	runOnce(context.Background(), mq, pub, cfg, testLog())

	mq.AssertExpectations(t)
	// Publisher must NOT be called when task is permanently failed.
	pub.AssertNotCalled(t, "Process", mock.Anything, mock.Anything)
}

// TestRunOnce_SkipsWhenNoStaleTasks verifies that when ListStaleTasks returns
// an empty slice, neither MarkStaleTaskPending nor Process are ever called.
func TestRunOnce_SkipsWhenNoStaleTasks(t *testing.T) {
	mq := new(storemock.MockQuerier)
	pub := new(mockPublisher)
	cfg := testCfg()

	mq.On("ListStaleTasks", mock.Anything, mock.MatchedBy(func(p repository.ListStaleTasksParams) bool {
		return p.Limit == int32(cfg.WatchdogBatchSize)
	})).
		Return([]repository.ListStaleTasksRow{}, nil)

	runOnce(context.Background(), mq, pub, cfg, testLog())

	mq.AssertExpectations(t)
	pub.AssertNotCalled(t, "Process", mock.Anything, mock.Anything)
	mq.AssertNotCalled(t, "MarkStaleTaskPending", mock.Anything, mock.Anything)
	mq.AssertNotCalled(t, "MarkStaleTaskFailed", mock.Anything, mock.Anything)
}

// TestRunOnce_SkipsAlreadyClaimedTask verifies that if MarkStaleTaskPending
// returns 0 rows (another instance claimed it first), Process is not called.
func TestRunOnce_SkipsAlreadyClaimedTask(t *testing.T) {
	mq := new(storemock.MockQuerier)
	pub := new(mockPublisher)
	cfg := testCfg()

	taskID := uuid.New()
	staleTask := repository.ListStaleTasksRow{
		ID:         taskID,
		DocumentID: uuid.New(),
		ModuleName: "convert",
		RetryCount: 1,
	}

	mq.On("ListStaleTasks", mock.Anything, mock.MatchedBy(func(p repository.ListStaleTasksParams) bool {
		return p.Limit == int32(cfg.WatchdogBatchSize)
	})).
		Return([]repository.ListStaleTasksRow{staleTask}, nil)
	// 0 rows = another watchdog instance claimed it first.
	mq.On("MarkStaleTaskPending", mock.Anything, mock.MatchedBy(func(p repository.MarkStaleTaskPendingParams) bool {
		return p.ID == taskID
	})).Return(int64(0), nil)

	runOnce(context.Background(), mq, pub, cfg, testLog())

	mq.AssertExpectations(t)
	pub.AssertNotCalled(t, "Process", mock.Anything, mock.Anything)
}

// TestRunOnce_BestEffortOnPublishError verifies that a Process error does not
// propagate — the task was already claimed (pending), so the watchdog moves on.
func TestRunOnce_BestEffortOnPublishError(t *testing.T) {
	mq := new(storemock.MockQuerier)
	pub := new(mockPublisher)
	cfg := testCfg()

	taskID := uuid.New()
	docID := uuid.New()
	staleTask := repository.ListStaleTasksRow{
		ID:               taskID,
		DocumentID:       docID,
		ModuleName:       "convert",
		RetryCount:       0,
		InputStoragePath: "bucket/docs/test.pdf",
	}

	mq.On("ListStaleTasks", mock.Anything, mock.MatchedBy(func(p repository.ListStaleTasksParams) bool {
		return p.Limit == int32(cfg.WatchdogBatchSize)
	})).
		Return([]repository.ListStaleTasksRow{staleTask}, nil)
	mq.On("MarkStaleTaskPending", mock.Anything, mock.MatchedBy(func(p repository.MarkStaleTaskPendingParams) bool {
		return p.ID == taskID
	})).Return(int64(1), nil)
	// Retry-loop: Process is called up to maxPublishAttempts (3) times.
	pub.On("Process", mock.Anything, mock.Anything).Return(context.DeadlineExceeded).Times(3)

	// Must not panic or return error.
	require.NotPanics(t, func() {
		runOnce(context.Background(), mq, pub, cfg, testLog())
	})

	mq.AssertExpectations(t)
	pub.AssertExpectations(t)
}

// TestRunOnce_RequeuesStaleTaskInPendingStatus verifies that a task stuck in
// 'pending' (e.g. Redis message lost before worker picked it up, or worker
// failed before updating status via callback) is also re-queued by the watchdog.
func TestRunOnce_RequeuesStaleTaskInPendingStatus(t *testing.T) {
	mq := new(storemock.MockQuerier)
	pub := new(mockPublisher)
	cfg := testCfg()

	taskID := uuid.New()
	docID := uuid.New()
	staleTask := repository.ListStaleTasksRow{
		ID:               taskID,
		DocumentID:       docID,
		ModuleName:       "convert",
		RetryCount:       0,
		InputStoragePath: "bucket/docs/test.pdf",
	}

	mq.On("ListStaleTasks", mock.Anything, mock.MatchedBy(func(p repository.ListStaleTasksParams) bool {
		return p.Limit == int32(cfg.WatchdogBatchSize)
	})).
		Return([]repository.ListStaleTasksRow{staleTask}, nil)
	mq.On("MarkStaleTaskPending", mock.Anything, mock.MatchedBy(func(p repository.MarkStaleTaskPendingParams) bool {
		return p.ID == taskID
	})).Return(int64(1), nil)
	pub.On("Process", mock.Anything, pythonworker.ProcessRequest{
		TaskID:      taskID.String(),
		DocumentID:  docID.String(),
		ModuleName:  "convert",
		StoragePath: "bucket/docs/test.pdf",
	}).Return(nil)

	runOnce(context.Background(), mq, pub, cfg, testLog())

	mq.AssertExpectations(t)
	pub.AssertExpectations(t)
}
