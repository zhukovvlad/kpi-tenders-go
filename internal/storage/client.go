package storage

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"go-kpi-tenders/internal/config"
)

// Client wraps MinIO and exposes a minimal interface for document storage.
type Client struct {
	mc     *minio.Client
	bucket string
}

// New creates a MinIO-backed storage client from application config.
func New(cfg *config.Config) (*Client, error) {
	mc, err := minio.New(cfg.S3Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		Secure: cfg.S3UseSSL,
		Region: cfg.S3Region,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: init minio client: %w", err)
	}
	return &Client{mc: mc, bucket: cfg.S3Bucket}, nil
}

// Upload streams r into the default bucket under a freshly generated UUID key.
// The object name is "{uuid}{ext}" where ext is derived from originalFilename.
// Returns storagePath in the format "{bucket}/{object_name}" — the same
// convention used by the Python worker (see app/storage/minio_client.py).
func (c *Client) Upload(ctx context.Context, r io.Reader, size int64, originalFilename, contentType string) (storagePath string, err error) {
	ext := filepath.Ext(originalFilename)
	objectName := uuid.New().String() + strings.ToLower(ext)

	_, err = c.mc.PutObject(ctx, c.bucket, objectName, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("storage: put object %q: %w", objectName, err)
	}

	return c.bucket + "/" + objectName, nil
}

// PresignedURL returns a time-limited presigned GET URL for the given storagePath.
// storagePath must be in the format returned by Upload: "{bucket}/{object_name}".
func (c *Client) PresignedURL(ctx context.Context, storagePath string, ttl time.Duration) (string, error) {
	objectName, err := c.objectNameFrom(storagePath)
	if err != nil {
		return "", err
	}

	u, err := c.mc.PresignedGetObject(ctx, c.bucket, objectName, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("storage: presign %q: %w", objectName, err)
	}
	return u.String(), nil
}

// Delete removes the object at storagePath from the bucket.
// It is a best-effort operation; callers may choose to log and continue on error.
func (c *Client) Delete(ctx context.Context, storagePath string) error {
	objectName, err := c.objectNameFrom(storagePath)
	if err != nil {
		return err
	}
	if err := c.mc.RemoveObject(ctx, c.bucket, objectName, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: delete object %q: %w", objectName, err)
	}
	return nil
}

// objectNameFrom strips the leading "{bucket}/" prefix from storagePath.
func (c *Client) objectNameFrom(storagePath string) (string, error) {
	prefix := c.bucket + "/"
	if !strings.HasPrefix(storagePath, prefix) {
		return "", fmt.Errorf("storage: storagePath %q does not start with bucket %q", storagePath, c.bucket)
	}
	objectName := strings.TrimPrefix(storagePath, prefix)
	if objectName == "" {
		return "", fmt.Errorf("storage: storagePath %q has empty object name", storagePath)
	}
	return objectName, nil
}
