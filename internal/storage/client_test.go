package storage_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/storage"
)

// newTestClient returns a Client configured against a non-existent MinIO
// endpoint. Safe for tests that exercise pure logic or error wrapping:
// minio-go creates the client struct without connecting, so no network
// traffic occurs until an operation is actually attempted.
func newTestClient(t *testing.T) *storage.Client {
	t.Helper()
	cfg := &config.Config{
		S3Endpoint:  "localhost:19999",
		S3AccessKey: "minioadmin",
		S3SecretKey: "minioadmin",
		S3Bucket:    "tenders",
		S3Region:    "us-east-1",
		S3UseSSL:    false,
	}
	c, err := storage.New(cfg)
	require.NoError(t, err)
	return c
}

// ── PresignedURL ──────────────────────────────────────────────────────────────

func TestPresignedURL_BadStoragePath_ReturnsError(t *testing.T) {
	c := newTestClient(t)

	// Path that does not start with the configured bucket "tenders/".
	_, err := c.PresignedURL(context.Background(), "other-bucket/uuid.pdf", time.Hour)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenders")
}

func TestPresignedURL_MissingSlash_ReturnsError(t *testing.T) {
	c := newTestClient(t)

	// No "/" at all — also an invalid storage path.
	_, err := c.PresignedURL(context.Background(), "tenders", time.Hour)

	require.Error(t, err)
}

func TestPresignedURL_ValidStoragePath_ReturnsURL(t *testing.T) {
	c := newTestClient(t)

	// minio-go computes the presigned URL locally (HMAC signing, no network).
	url, err := c.PresignedURL(context.Background(), "tenders/some-uuid.pdf", time.Hour)

	require.NoError(t, err)
	assert.Contains(t, url, "some-uuid.pdf")
}

// ── Upload ────────────────────────────────────────────────────────────────────

func TestUpload_WrapsMinIOError(t *testing.T) {
	c := newTestClient(t)

	// Upload will fail because the endpoint does not exist.
	// We only verify that our error wrapping is applied correctly.
	_, err := c.Upload(
		context.Background(),
		strings.NewReader("hello"),
		5,
		"test.pdf",
		"application/pdf",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage: put object")
}
