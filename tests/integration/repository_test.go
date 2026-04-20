//go:build integration

package integration

import (
	"context"
	"fmt"
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
		UploadedBy:     userID,
		FileName:       "test-doc-" + uuid.New().String() + ".pdf",
		StoragePath:    "test-bucket/docs/" + uuid.New().String() + ".pdf",
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
	assert.True(t, org.IsActive)
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

// ── Construction site tests ───────────────────────────────────────────────────

func TestRepository_CreateConstructionSite(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)

	site, err := q.CreateConstructionSite(ctx, repository.CreateConstructionSiteParams{
		OrganizationID: org.ID,
		Name:           "ЖК Верейская",
		Status:         "active",
		CreatedBy:      pgtype.UUID{Bytes: user.ID, Valid: true},
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, site.ID)
	assert.Equal(t, "ЖК Верейская", site.Name)
	assert.Equal(t, "active", site.Status)
	assert.False(t, site.ParentID.Valid)
}

func TestRepository_CreateConstructionSite_WithParent(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)

	parent, err := q.CreateConstructionSite(ctx, repository.CreateConstructionSiteParams{
		OrganizationID: org.ID,
		Name:           "ЖК Верейская",
		Status:         "active",
	})
	require.NoError(t, err)

	child, err := q.CreateConstructionSite(ctx, repository.CreateConstructionSiteParams{
		OrganizationID: org.ID,
		ParentID:       pgtype.UUID{Bytes: parent.ID, Valid: true},
		Name:           "1-я очередь",
		Status:         "active",
	})
	require.NoError(t, err)
	assert.Equal(t, parent.ID, uuid.UUID(child.ParentID.Bytes))
}

func TestRepository_ListConstructionSitesByOrganization(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)

	for i := range 3 {
		_, err := q.CreateConstructionSite(ctx, repository.CreateConstructionSiteParams{
			OrganizationID: org.ID,
			Name:           fmt.Sprintf("Site %d", i),
			Status:         "active",
		})
		require.NoError(t, err)
	}

	sites, err := q.ListConstructionSitesByOrganization(ctx, org.ID)
	require.NoError(t, err)
	assert.Len(t, sites, 3)
}

// ── Document tests ────────────────────────────────────────────────────────────

func TestRepository_CreateDocument(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)

	doc, err := q.CreateDocument(ctx, repository.CreateDocumentParams{
		OrganizationID: org.ID,
		UploadedBy:     user.ID,
		FileName:       "tender.pdf",
		StoragePath:    "docs/tender.pdf",
		MimeType:       pgtype.Text{String: "application/pdf", Valid: true},
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, doc.ID)
	assert.Equal(t, "tender.pdf", doc.FileName)
	assert.Equal(t, "application/pdf", doc.MimeType.String)
	assert.False(t, doc.SiteID.Valid)
}

func TestRepository_ListDocumentsBySite(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)
	site, err := q.CreateConstructionSite(ctx, repository.CreateConstructionSiteParams{
		OrganizationID: org.ID, Name: "Test Site", Status: "active",
	})
	require.NoError(t, err)

	for range 2 {
		_, err := q.CreateDocument(ctx, repository.CreateDocumentParams{
			OrganizationID: org.ID,
			SiteID:         pgtype.UUID{Bytes: site.ID, Valid: true},
			UploadedBy:     user.ID,
			FileName:       "doc-" + uuid.New().String() + ".pdf",
			StoragePath:    "docs/" + uuid.New().String(),
		})
		require.NoError(t, err)
	}

	docs, err := q.ListDocumentsBySite(ctx, pgtype.UUID{Bytes: site.ID, Valid: true})
	require.NoError(t, err)
	assert.Len(t, docs, 2)
}
