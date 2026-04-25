package pythonworker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Publisher publishes Celery tasks directly to Redis queues.
// This bypasses the Python HTTP API and ensures tasks are queued even when
// Python workers are temporarily unavailable.
type Publisher struct {
	rdb *redis.Client
}

// New creates a Publisher connected to the given Redis URL.
func New(redisURL string) (*Publisher, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("pythonworker: parse redis URL: %w", err)
	}
	return &Publisher{rdb: redis.NewClient(opts)}, nil
}

// ProcessRequest describes the Celery task payload.
type ProcessRequest struct {
	TaskID      string
	DocumentID  string
	ModuleName  string
	StoragePath string
}

// Process publishes a Celery v2 task message to the appropriate Redis queue.
func (p *Publisher) Process(ctx context.Context, req ProcessRequest) error {
	queue, taskName, err := resolveModule(req.ModuleName)
	if err != nil {
		return err
	}

	taskUUID := uuid.New().String()

	// Celery protocol v2 body: [args, kwargs, embed]
	bodyArgs := []any{
		[]any{req.TaskID, req.DocumentID, req.StoragePath},
		map[string]any{},
		map[string]any{
			"callbacks": nil,
			"errbacks":  nil,
			"chain":     nil,
			"chord":     nil,
		},
	}
	bodyJSON, err := json.Marshal(bodyArgs)
	if err != nil {
		return fmt.Errorf("pythonworker: marshal body: %w", err)
	}

	msg := map[string]any{
		"body":             base64.StdEncoding.EncodeToString(bodyJSON),
		"content-encoding": "utf-8",
		"content-type":     "application/json",
		"headers": map[string]any{
			"lang":        "py",
			"task":        taskName,
			"id":          taskUUID,
			"shadow":      nil,
			"eta":         nil,
			"expires":     nil,
			"group":       nil,
			"group_index": nil,
			"retries":     0,
			"timelimit":   []any{nil, nil},
			"root_id":     taskUUID,
			"parent_id":   nil,
			"argsrepr":    fmt.Sprintf("('%s', '%s', '%s')", req.TaskID, req.DocumentID, req.StoragePath),
			"kwargsrepr":  "{}",
			"origin":      "go-kpi-tenders",
		},
		"properties": map[string]any{
			"correlation_id": taskUUID,
			"reply_to":       uuid.New().String(),
			"delivery_mode":  2,
			"delivery_info": map[string]any{
				"exchange":    "",
				"routing_key": queue,
			},
			"priority":      0,
			"body_encoding": "base64",
			"delivery_tag":  uuid.New().String(),
		},
	}

	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("pythonworker: marshal message: %w", err)
	}

	if err := p.rdb.LPush(ctx, queue, string(msgJSON)).Err(); err != nil {
		return fmt.Errorf("pythonworker: lpush %s: %w", queue, err)
	}
	return nil
}

// resolveModule maps a module name to its Redis queue and Celery task name.
func resolveModule(module string) (queue, taskName string, err error) {
	switch module {
	case "convert":
		return "io", "app.workers.convert.convert_task", nil
	case "parse_invoice":
		return "io", "app.workers.parse_invoice.parse_invoice_task", nil
	case "anonymize":
		return "llm", "app.workers.anonymize.anonymize_task", nil
	case "extract":
		return "llm", "app.workers.extract.extract_task", nil
	default:
		return "", "", fmt.Errorf("pythonworker: unknown module %q", module)
	}
}
