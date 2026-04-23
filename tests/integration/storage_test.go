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

// testStorageClient builds a *storage.Client backed by the ephemeral MinIO
// testcontainer started in TestMain and ensures the target bucket exists.
func testStorageClient(t *testing.T) *storage.Client {
	t.Helper()

	cfg := &config.Config{
		S3Endpoint:  testMinioEndpoint,
		S3AccessKey: testMinioAccessKey,
		S3SecretKey: testMinioSecretKey,
		S3Bucket:    testMinioBucket,
		S3Region:    "us-east-1",
		S3UseSSL:    false,
	}

	sc, err := storage.New(cfg)
	require.NoError(t, err, "storage.New must succeed against testcontainer MinIO")

	ensureBucket(t)

	return sc
}

// ensureBucket creates the test bucket if it does not already exist.
func ensureBucket(t *testing.T) {
	t.Helper()

	mc, err := minio.New(testMinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(testMinioAccessKey, testMinioSecretKey, ""),
		Secure: false,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exists, err := mc.BucketExists(ctx, testMinioBucket)
	require.NoError(t, err)
	if !exists {
		err = mc.MakeBucket(ctx, testMinioBucket, minio.MakeBucketOptions{Region: "us-east-1"})
		require.NoError(t, err, "failed to create bucket %q", testMinioBucket)
	}
}

// ── Upload ────────────────────────────────────────────────────────────────────

func TestStorageUpload_ReturnsStoragePath(t *testing.T) {
	sc := testStorageClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	content := strings.NewReader("integration test content")
	storagePath, err := sc.Upload(ctx, content, int64(content.Len()), "test.txt", "text/plain")
	require.NoError(t, err)
	// Format must be "{bucket}/{uuid}.txt"
	assert.True(t, strings.HasPrefix(storagePath, testMinioBucket+"/"),
		"storagePath %q must start with %q/", storagePath, testMinioBucket)
	assert.True(t, strings.HasSuffix(storagePath, ".txt"),
		"storagePath %q must preserve extension", storagePath)
}

func TestStorageUpload_ObjectExistsInMinIO(t *testing.T) {
	sc := testStorageClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	content := strings.NewReader("object existence check")
	storagePath, err := sc.Upload(ctx, content, int64(content.Len()), "check.txt", "text/plain")
	require.NoError(t, err)

	// Verify the object is actually present in MinIO using raw client.
	mc, err := minio.New(testMinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(testMinioAccessKey, testMinioSecretKey, ""),
		Secure: false,
	})
	require.NoError(t, err)

	objectName := strings.TrimPrefix(storagePath, testMinioBucket+"/")
	info, err := mc.StatObject(ctx, testMinioBucket, objectName, minio.StatObjectOptions{})
	require.NoError(t, err, "object %q must exist in MinIO", objectName)
	assert.Equal(t, int64(len("object existence check")), info.Size)
}

// ── PresignedURL ──────────────────────────────────────────────────────────────

func TestStoragePresignedURL_ReturnsReachableURL(t *testing.T) {
	sc := testStorageClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	content := strings.NewReader("presigned url test")
	storagePath, err := sc.Upload(ctx, content, int64(content.Len()), "presign.txt", "text/plain")
	require.NoError(t, err)

	url, err := sc.PresignedURL(ctx, storagePath, 5*time.Minute)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(url, "http"), "URL must be http(s): %q", url)

	// Use a client with timeout so the test fails fast if MinIO is unreachable.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"presigned URL must return 200, got %d", resp.StatusCode)
}

func TestStoragePresignedURL_WrongBucket_ReturnsError(t *testing.T) {
	sc := testStorageClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := sc.PresignedURL(ctx, "wrong-bucket/some-object.pdf", time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage:")
}
