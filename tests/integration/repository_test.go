//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"go-kpi-tenders/internal/pgutil"
	"go-kpi-tenders/internal/repository"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

var innCounter atomic.Int64

func uniqueINN() string {
	return fmt.Sprintf("%010d", innCounter.Add(1)+1_000_000_000)
}

func createTestOrg(t *testing.T, ctx context.Context) repository.Organization {
	t.Helper()
	q := repository.New(testPool)
	org, err := q.CreateOrganization(ctx, repository.CreateOrganizationParams{Name: "TestOrg-" + uuid.New().String()})
	require.NoError(t, err)
	return org
}

func createTestUser(t *testing.T, ctx context.Context, orgID uuid.UUID) repository.User {
	t.Helper()
	q := repository.New(testPool)
	hash, err := bcrypt.GenerateFromPassword([]byte("test"), bcrypt.MinCost)
	require.NoError(t, err)
	user, err := q.CreateUser(ctx, repository.CreateUserParams{
		OrganizationID: orgID,
		Email:          fmt.Sprintf("user-%s@test.com", uuid.New()),
		PasswordHash:   string(hash),
		FullName:       "Test User",
		Role:           "admin",
	})
	require.NoError(t, err)
	return user
}

func createTestDocument(t *testing.T, ctx context.Context, orgID, userID uuid.UUID) repository.Document {
	t.Helper()
	q := repository.New(testPool)
	doc, err := q.CreateDocument(ctx, repository.CreateDocumentParams{
		OrganizationID: orgID,
		Title:          "Test Doc " + uuid.New().String(),
		FilePath:       "/test/doc.pdf",
		Status:         "pending",
		UploadedBy:     userID,
	})
	require.NoError(t, err)
	return doc
}

// ── Organization tests ────────────────────────────────────────────────────────

func TestRepository_CreateOrganization(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)

	org, err := q.CreateOrganization(ctx, repository.CreateOrganizationParams{
		Name: "Acme Corp",
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, org.ID)
	assert.Equal(t, "Acme Corp", org.Name)
	assert.False(t, org.Inn.Valid)
}

func TestRepository_CreateOrganization_WithINN(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	inn := uniqueINN()

	org, err := q.CreateOrganization(ctx, repository.CreateOrganizationParams{
		Name: "INN Corp",
		Inn:  pgtype.Text{String: inn, Valid: true},
	})

	require.NoError(t, err)
	assert.Equal(t, inn, org.Inn.String)
}

func TestRepository_CreateOrganization_DuplicateINN(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	inn := pgtype.Text{String: uniqueINN(), Valid: true}

	_, err := q.CreateOrganization(ctx, repository.CreateOrganizationParams{Name: "First", Inn: inn})
	require.NoError(t, err)

	_, err = q.CreateOrganization(ctx, repository.CreateOrganizationParams{Name: "Second", Inn: inn})
	require.Error(t, err)
	assert.True(t, pgutil.IsUniqueViolation(err, "organizations_inn_key"))
}

func TestRepository_GetOrganizationByID_NotFound(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)

	_, err := q.GetOrganizationByID(ctx, uuid.New())
	require.Error(t, err)
}

func TestRepository_CreateAndGetOrganization(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)

	created, err := q.CreateOrganization(ctx, repository.CreateOrganizationParams{Name: "Round-trip Org"})
	require.NoError(t, err)

	fetched, err := q.GetOrganizationByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, "Round-trip Org", fetched.Name)
}

// ── User tests ────────────────────────────────────────────────────────────────

func TestRepository_CreateUser_DuplicateEmail(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)
	hash, err := bcrypt.GenerateFromPassword([]byte("test"), bcrypt.MinCost)
	require.NoError(t, err)

	email := fmt.Sprintf("dup-%s@test.com", uuid.New())
	params := repository.CreateUserParams{
		OrganizationID: org.ID, Email: email,
		PasswordHash: string(hash), FullName: "User", Role: "member",
	}

	_, err = q.CreateUser(ctx, params)
	require.NoError(t, err)

	_, err = q.CreateUser(ctx, params)
	require.Error(t, err)
	assert.True(t, pgutil.IsUniqueViolation(err, "users_email_key"))
}

// ── RAG: catalog_positions vector search ─────────────────────────────────────

type catalogPosition struct {
	Title      string
	Embedding  []float32
	Parameters map[string]any
}

func insertCatalogPosition(t *testing.T, ctx context.Context, docID uuid.UUID, p catalogPosition) uuid.UUID {
	t.Helper()

	paramsJSON, err := json.Marshal(p.Parameters)
	require.NoError(t, err)

	// Build the vector literal: [f1,f2,...]
	vec := fmt.Sprintf("%v", p.Embedding)
	// fmt gives [a b c] → need [a,b,c]
	vecStr := strings.Replace(vec, " ", ",", -1)

	const q = `
		INSERT INTO catalog_positions (document_id, title, embedding, parameters)
		VALUES ($1, $2, $3::vector, $4)
		RETURNING id`

	var id uuid.UUID
	err = testPool.QueryRow(ctx, q, docID, p.Title, vecStr, paramsJSON).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestCatalogPositions_RAGSearch_CosineSimilarity(t *testing.T) {
	ctx := context.Background()

	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)
	doc := createTestDocument(t, ctx, org.ID, user.ID)

	positions := []catalogPosition{
		{
			Title:      "Steel Beam IPE-200",
			Embedding:  []float32{0.9, 0.1, 0.1},
			Parameters: map[string]any{"material": "steel", "type": "beam", "size": "IPE-200"},
		},
		{
			Title:      "Concrete B25",
			Embedding:  []float32{0.1, 0.9, 0.1},
			Parameters: map[string]any{"material": "concrete", "grade": "B25"},
		},
		{
			Title:      "Steel Column HEA-200",
			Embedding:  []float32{0.85, 0.15, 0.1},
			Parameters: map[string]any{"material": "steel", "type": "column", "size": "HEA-200"},
		},
	}
	for _, p := range positions {
		insertCatalogPosition(t, ctx, doc.ID, p)
	}

	// Query vector closest to "Steel Beam" (0.9, 0.1, 0.1).
	// Only steel items should pass the JSONB filter.
	const searchSQL = `
		SELECT title, 1 - (embedding <=> $1::vector) AS similarity
		FROM catalog_positions
		WHERE document_id = $2
		  AND parameters @> $3::jsonb
		ORDER BY embedding <=> $1::vector
		LIMIT $4`

	queryVec := "[0.88,0.12,0.10]"
	filterJSON := `{"material": "steel"}`

	rows, err := testPool.Query(ctx, searchSQL, queryVec, doc.ID, filterJSON, 10)
	require.NoError(t, err)
	defer rows.Close()

	type result struct {
		Title      string
		Similarity float64
	}
	var results []result
	for rows.Next() {
		var r result
		require.NoError(t, rows.Scan(&r.Title, &r.Similarity))
		results = append(results, r)
	}
	require.NoError(t, rows.Err())

	require.Len(t, results, 2, "only steel positions should be returned")
	assert.Equal(t, "Steel Beam IPE-200", results[0].Title, "beam must rank higher than column")
	assert.Greater(t, results[0].Similarity, 0.995, "high cosine similarity expected")
}

func TestCatalogPositions_JSONBFilter(t *testing.T) {
	ctx := context.Background()

	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)
	doc := createTestDocument(t, ctx, org.ID, user.ID)

	for _, p := range []catalogPosition{
		{Title: "Wood Beam", Embedding: []float32{0.5, 0.5, 0.0}, Parameters: map[string]any{"material": "wood"}},
		{Title: "Brick Wall", Embedding: []float32{0.3, 0.6, 0.1}, Parameters: map[string]any{"material": "brick"}},
	} {
		insertCatalogPosition(t, ctx, doc.ID, p)
	}

	// Filter should return zero steel results from this document.
	const q = `SELECT count(*) FROM catalog_positions WHERE document_id = $1 AND parameters @> '{"material":"steel"}'::jsonb`
	var count int
	require.NoError(t, testPool.QueryRow(ctx, q, doc.ID).Scan(&count))
	assert.Equal(t, 0, count)
}
