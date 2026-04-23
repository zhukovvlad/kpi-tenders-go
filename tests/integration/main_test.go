//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testPool is shared across all integration tests in this package.
var testPool *pgxpool.Pool

// testMinioEndpoint, testMinioAccessKey, testMinioSecretKey are set in TestMain
// from the ephemeral MinIO testcontainer and shared across storage tests.
var (
	testMinioEndpoint  string
	testMinioAccessKey = "minioadmin"
	testMinioSecretKey = "minioadmin"
	testMinioBucket    = "tenders"
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	pgContainer, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("kpi_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start postgres container: %v\n", err)
		return 1
	}
	defer func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to terminate container: %v\n", err)
		}
	}()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get connection string: %v\n", err)
		return 1
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create pool: %v\n", err)
		return 1
	}
	defer pool.Close()

	if err := applyMigrations(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "failed to apply migrations: %v\n", err)
		return 1
	}

	testPool = pool

	// ── MinIO ─────────────────────────────────────────────────────────────────
	minioContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "minio/minio:latest",
			ExposedPorts: []string{"9000/tcp"},
			Env: map[string]string{
				"MINIO_ROOT_USER":     testMinioAccessKey,
				"MINIO_ROOT_PASSWORD": testMinioSecretKey,
			},
			Cmd:        []string{"server", "/data"},
			WaitingFor: wait.ForHTTP("/minio/health/live").WithPort("9000/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start minio container: %v\n", err)
		return 1
	}
	defer func() {
		if err := minioContainer.Terminate(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to terminate minio container: %v\n", err)
		}
	}()

	minioHost, err := minioContainer.Host(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get minio host: %v\n", err)
		return 1
	}
	minioPort, err := minioContainer.MappedPort(ctx, "9000/tcp")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get minio port: %v\n", err)
		return 1
	}
	testMinioEndpoint = fmt.Sprintf("%s:%s", minioHost, minioPort.Port())

	return m.Run()
}

// applyMigrations reads all *.up.sql files from sql/migrations/ in filename
// order and executes them against the given pool.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("runtime.Caller failed")
	}
	// tests/integration/ → ../../sql/migrations/
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "..", "sql", "migrations")

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("reading migrations dir: %w", err)
	}

	var upFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			upFiles = append(upFiles, filepath.Join(migrationsDir, e.Name()))
		}
	}
	sort.Strings(upFiles)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring migration connection: %w", err)
	}
	defer conn.Release()

	for _, path := range upFiles {
		sql, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		if _, err := conn.Conn().PgConn().Exec(ctx, string(sql)).ReadAll(); err != nil {
			return fmt.Errorf("applying %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}
