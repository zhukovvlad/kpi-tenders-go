package server

import (
	"context"
	"io"
	"net/url"
	"time"
)

// storageClient abstracts the S3/MinIO operations used by handlers.
// Storing an interface instead of *storage.Client lets handler tests stub
// Upload, PresignedURLWithParams, and Delete without a live MinIO endpoint.
type storageClient interface {
	Upload(ctx context.Context, r io.Reader, size int64, originalFilename, contentType string) (storagePath string, err error)
	PresignedURLWithParams(ctx context.Context, storagePath string, ttl time.Duration, params url.Values) (string, error)
	Delete(ctx context.Context, storagePath string) error
}
