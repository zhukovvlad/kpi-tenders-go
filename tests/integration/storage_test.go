//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/storage"
)

// minioEndpoint is the live MinIO instance started by docker-compose.
const (
	minioEndpoint  = "localhost:9000"
	minioAccessKey = "minioadmin"
	minioSecretKey = "minioadmin"
	minioBucket    = "tenders"
)

// testStorageClient builds a *storage.Client and ensures the target bucket
// exists before returning. The bucket is left intact after tests — it belongs
// to the shared dev MinIO, not a throwaway container.
func testStorageClient(t *testing.T) *storage.Client {
	t.Helper()

	cfg := &config.Config{
		S3Endpoint:  minioEndpoint,
		S3AccessKey: minioAccessKey,
		S3SecretKey: minioSecretKey,
		S3Bucket:    minioBucket,
		S3Region:    "us-east-1",
		S3UseSSL:    false,
	}

	sc, err := storage.New(cfg)
	require.NoError(t, err, "storage.New must succeed against live MinIO")

	// Ensure the bucket exists so tests don't fail due to missing bucket.
	ensureBucket(t, minioBucket)

	return sc
}

// ensureBucket creates the bucket if it does not exist.
func ensureBucket(t *testing.T, bucket string) {
	t.Helper()

	mc, err := minio.New(minioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(minioAccessKey, minioSecretKey, ""),
		Secure: false,
	})
	require.NoError(t, err)

	ctx := context.Background()
	exists, err := mc.BucketExists(ctx, bucket)
	require.NoError(t, err)
	if !exists {
		err = mc.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: "us-east-1"})
		require.NoError(t, err, "failed to create bucket %q", bucket)
	}
}

// ── Upload ────────────────────────────────────────────────────────────────────

func TestStorageUpload_ReturnsStoragePath(t *testing.T) {
	sc := testStorageClient(t)
	ctx := context.Background()

	content := strings.NewReader("integration test content")
	storagePath, err := sc.Upload(ctx, content, int64(content.Len()), "test.txt", "text/plain")

	require.NoError(t, err)
	// Format must be "{bucket}/{uuid}.txt"
	assert.True(t, strings.HasPrefix(storagePath, minioBucket+"/"),
		"storagePath %q must start with %q/", storagePath, minioBucket)
	assert.True(t, strings.HasSuffix(storagePath, ".txt"),
		"storagePath %q must preserve extension", storagePath)
}

func TestStorageUpload_ObjectExistsInMinIO(t *testing.T) {
	sc := testStorageClient(t)
	ctx := context.Background()

	content := strings.NewReader("object existence check")
	storagePath, err := sc.Upload(ctx, content, int64(content.Len()), "check.txt", "text/plain")
	require.NoError(t, err)

	// Verify the object is actually present in MinIO using raw client.
	mc, err := minio.New(minioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(minioAccessKey, minioSecretKey, ""),
		Secure: false,
	})
	require.NoError(t, err)

	// objectName is the part after "{bucket}/"
	objectName := strings.TrimPrefix(storagePath, minioBucket+"/")
	info, err := mc.StatObject(ctx, minioBucket, objectName, minio.StatObjectOptions{})
	require.NoError(t, err, "object %q must exist in MinIO", objectName)
	assert.Equal(t, int64(len("object existence check")), info.Size)
}

// ── PresignedURL ──────────────────────────────────────────────────────────────

func TestStoragePresignedURL_ReturnsReachableURL(t *testing.T) {
	sc := testStorageClient(t)
	ctx := context.Background()

	// First upload an object so we have something to sign.
	content := strings.NewReader("presigned url test")
	storagePath, err := sc.Upload(ctx, content, int64(content.Len()), "presign.txt", "text/plain")
	require.NoError(t, err)

	url, err := sc.PresignedURL(ctx, storagePath, 5*time.Minute)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(url, "http"), "URL must be http(s): %q", url)

	// The presigned URL must be reachable and return 200.
	resp, err := http.Get(url) //nolint:noctx
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"presigned URL must return 200, got %d", resp.StatusCode)
}

func TestStoragePresignedURL_WrongBucket_ReturnsError(t *testing.T) {
	sc := testStorageClient(t)
	ctx := context.Background()

	_, err := sc.PresignedURL(ctx, "wrong-bucket/some-object.pdf", time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage:")
}
