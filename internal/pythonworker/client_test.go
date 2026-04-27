package pythonworker

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildCeleryMessage_FieldsMatchRequest verifies that buildCeleryMessage
// produces a Celery v2 envelope where:
//   - headers.id/root_id and properties.correlation_id carry req.TaskID (not a
//     random UUID), enabling tracing across Go ↔ Celery;
//   - headers.task and delivery_info.routing_key match the resolved module;
//   - the base64-decoded body decodes to the expected positional args.
func TestBuildCeleryMessage_FieldsMatchRequest(t *testing.T) {
	req := ProcessRequest{
		TaskID:      "task-uuid-abc",
		DocumentID:  "doc-uuid-xyz",
		ModuleName:  "convert",
		StoragePath: "tenders/doc.pdf",
	}

	queue, taskName, err := resolveModule(req.ModuleName)
	require.NoError(t, err)

	msgBytes, err := buildCeleryMessage(req, queue, taskName, "reply-uuid", "delivery-uuid")
	require.NoError(t, err)

	var msg map[string]any
	require.NoError(t, json.Unmarshal(msgBytes, &msg))

	// ── Headers ──────────────────────────────────────────────────────────────
	headers, ok := msg["headers"].(map[string]any)
	require.True(t, ok, "headers must be a JSON object")

	assert.Equal(t, taskName, headers["task"], "headers.task")
	assert.Equal(t, req.TaskID, headers["id"], "headers.id must equal req.TaskID for tracing")
	assert.Equal(t, req.TaskID, headers["root_id"], "headers.root_id must equal req.TaskID")
	assert.Equal(t, "go-kpi-tenders", headers["origin"])

	// ── Properties ───────────────────────────────────────────────────────────
	props, ok := msg["properties"].(map[string]any)
	require.True(t, ok, "properties must be a JSON object")

	assert.Equal(t, req.TaskID, props["correlation_id"], "correlation_id must equal req.TaskID")
	assert.Equal(t, "reply-uuid", props["reply_to"], "reply_to must equal the value passed by caller")
	assert.Equal(t, float64(2), props["delivery_mode"], "delivery_mode must be 2 (persistent)")

	di, ok := props["delivery_info"].(map[string]any)
	require.True(t, ok, "delivery_info must be a JSON object")
	assert.Equal(t, queue, di["routing_key"], "routing_key must match resolved queue")

	// ── Body: base64 → JSON → positional args ────────────────────────────────
	bodyB64, ok := msg["body"].(string)
	require.True(t, ok, "body must be a base64 string")

	bodyJSON, err := base64.StdEncoding.DecodeString(bodyB64)
	require.NoError(t, err, "body must be valid base64")

	var bodyArgs []any
	require.NoError(t, json.Unmarshal(bodyJSON, &bodyArgs), "body must be valid JSON")
	require.Len(t, bodyArgs, 3, "Celery v2 body must have [args, kwargs, embed]")

	posArgs, ok := bodyArgs[0].([]any)
	require.True(t, ok, "first element must be the positional args array")
	require.Len(t, posArgs, 3)
	assert.Equal(t, req.TaskID, posArgs[0])
	assert.Equal(t, req.DocumentID, posArgs[1])
	assert.Equal(t, req.StoragePath, posArgs[2])
}

// TestBuildCeleryMessage_AllModules verifies that every supported module routes
// to the correct queue and Celery task name.
func TestBuildCeleryMessage_AllModules(t *testing.T) {
	cases := []struct {
		module        string
		expectedQueue string
		expectedTask  string
	}{
		{"convert", "io", "app.workers.convert.convert_task"},
		{"parse_invoice", "io", "app.workers.parse_invoice.parse_invoice_task"},
		{"anonymize", "llm", "app.workers.anonymize.anonymize_task"},
		{"resolve_keys", "llm", "app.workers.resolve_keys.resolve_keys_task"},
		{"extract", "llm", "app.workers.extract.extract_task"},
	}

	req := ProcessRequest{
		TaskID:      "t-id",
		DocumentID:  "d-id",
		StoragePath: "path/file.pdf",
	}

	for _, tc := range cases {
		t.Run(tc.module, func(t *testing.T) {
			queue, taskName, err := resolveModule(tc.module)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedQueue, queue)
			assert.Equal(t, tc.expectedTask, taskName)

			req.ModuleName = tc.module
			msgBytes, err := buildCeleryMessage(req, queue, taskName, "reply-uuid", "delivery-uuid")
			require.NoError(t, err)

			var msg map[string]any
			require.NoError(t, json.Unmarshal(msgBytes, &msg))

			headers := msg["headers"].(map[string]any)
			assert.Equal(t, tc.expectedTask, headers["task"])

			props := msg["properties"].(map[string]any)
			di := props["delivery_info"].(map[string]any)
			assert.Equal(t, tc.expectedQueue, di["routing_key"])
		})
	}
}

// TestBuildCeleryMessage_UnknownModuleIsRejected verifies that resolveModule
// (and by extension ValidateModule) returns an error for unknown modules.
func TestBuildCeleryMessage_UnknownModuleIsRejected(t *testing.T) {
	err := ValidateModule("not_a_module")
	assert.Error(t, err)
}

// TestBuildCeleryMessage_KwargsAreMergedIntoBody verifies that extra kwargs
// supplied via ProcessRequest.Kwargs appear in the Celery v2 body kwargs map.
// This is critical for resolve_keys and extract which need raw_questions,
// existing_keys, extraction_schema, and md_document_id passed as kwargs.
func TestBuildCeleryMessage_KwargsAreMergedIntoBody(t *testing.T) {
	req := ProcessRequest{
		TaskID:      "task-id",
		DocumentID:  "doc-id",
		ModuleName:  "resolve_keys",
		StoragePath: "tenders/doc.pdf",
		Kwargs: map[string]any{
			"raw_questions": []string{"What is the price?"},
			"existing_keys": []map[string]string{{"key_name": "price"}},
		},
	}

	queue, taskName, err := resolveModule(req.ModuleName)
	require.NoError(t, err)

	msgBytes, err := buildCeleryMessage(req, queue, taskName, "reply-id", "delivery-id")
	require.NoError(t, err)

	var msg map[string]any
	require.NoError(t, json.Unmarshal(msgBytes, &msg))

	bodyB64, ok := msg["body"].(string)
	require.True(t, ok)

	bodyJSON, err := base64.StdEncoding.DecodeString(bodyB64)
	require.NoError(t, err)

	var bodyArgs []any
	require.NoError(t, json.Unmarshal(bodyJSON, &bodyArgs))
	require.Len(t, bodyArgs, 3)

	kwargs, ok := bodyArgs[1].(map[string]any)
	require.True(t, ok, "second element must be the kwargs map")

	assert.Contains(t, kwargs, "raw_questions", "raw_questions must be in kwargs")
	assert.Contains(t, kwargs, "existing_keys", "existing_keys must be in kwargs")
}

// TestBuildCeleryMessage_NilKwargsProducesEmptyMap ensures that a nil Kwargs
// field does not cause a panic and results in an empty kwargs map in the body.
func TestBuildCeleryMessage_NilKwargsProducesEmptyMap(t *testing.T) {
	req := ProcessRequest{
		TaskID:      "t",
		DocumentID:  "d",
		ModuleName:  "convert",
		StoragePath: "p.pdf",
		Kwargs:      nil,
	}

	queue, taskName, err := resolveModule(req.ModuleName)
	require.NoError(t, err)

	msgBytes, err := buildCeleryMessage(req, queue, taskName, "r", "d2")
	require.NoError(t, err)

	var msg map[string]any
	require.NoError(t, json.Unmarshal(msgBytes, &msg))

	bodyJSON, _ := base64.StdEncoding.DecodeString(msg["body"].(string))
	var bodyArgs []any
	require.NoError(t, json.Unmarshal(bodyJSON, &bodyArgs))

	kwargs, ok := bodyArgs[1].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, kwargs, "nil Kwargs must produce an empty map, not nil")
}
