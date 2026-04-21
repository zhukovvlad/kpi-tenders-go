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

	docs, err := q.ListDocumentsBySite(ctx, repository.ListDocumentsBySiteParams{
		OrganizationID: org.ID,
		SiteID:         pgtype.UUID{Bytes: site.ID, Valid: true},
	})
	require.NoError(t, err)
	assert.Len(t, docs, 2)
}

// ── Tenant isolation (trigger) tests ─────────────────────────────────────────

func TestRepository_CreateConstructionSite_CrossTenantParent_IsRejected(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org1 := createTestOrg(t, ctx)
	org2 := createTestOrg(t, ctx)

	parent, err := q.CreateConstructionSite(ctx, repository.CreateConstructionSiteParams{
		OrganizationID: org1.ID, Name: "Parent Org1", Status: "active",
	})
	require.NoError(t, err)

	// org2 пытается использовать parent из org1 — триггер должен это заблокировать
	_, err = q.CreateConstructionSite(ctx, repository.CreateConstructionSiteParams{
		OrganizationID: org2.ID,
		ParentID:       pgtype.UUID{Bytes: parent.ID, Valid: true},
		Name:           "Child Org2",
		Status:         "active",
	})
	require.Error(t, err, "trigger must reject cross-tenant parent_id")
}

func TestRepository_CreateDocument_CrossTenantSite_IsRejected(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org1 := createTestOrg(t, ctx)
	org2 := createTestOrg(t, ctx)
	user2 := createTestUser(t, ctx, org2.ID)

	site, err := q.CreateConstructionSite(ctx, repository.CreateConstructionSiteParams{
		OrganizationID: org1.ID, Name: "Site Org1", Status: "active",
	})
	require.NoError(t, err)

	// org2 пытается создать документ с site_id из org1 — триггер должен заблокировать
	_, err = q.CreateDocument(ctx, repository.CreateDocumentParams{
		OrganizationID: org2.ID,
		SiteID:         pgtype.UUID{Bytes: site.ID, Valid: true},
		UploadedBy:     user2.ID,
		FileName:       "cross.pdf",
		StoragePath:    "docs/cross.pdf",
	})
	require.Error(t, err, "trigger must reject cross-tenant site_id")
}

// ── Document task tests ───────────────────────────────────────────────────────

func createTestSite(t *testing.T, ctx context.Context, orgID uuid.UUID) repository.ConstructionSite {
	t.Helper()
	q := repository.New(testPool)
	site, err := q.CreateConstructionSite(ctx, repository.CreateConstructionSiteParams{
		OrganizationID: orgID, Name: "Site-" + uuid.New().String(), Status: "active",
	})
	require.NoError(t, err)
	return site
}

func createTestTask(t *testing.T, ctx context.Context, docID, orgID uuid.UUID) repository.DocumentTask {
	t.Helper()
	q := repository.New(testPool)
	task, err := q.CreateDocumentTask(ctx, repository.CreateDocumentTaskParams{
		DocumentID: docID, ModuleName: "test_module", OrganizationID: orgID,
	})
	require.NoError(t, err)
	return task
}

func TestRepository_CreateDocumentTask(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)
	doc := createTestDocument(t, ctx, org.ID, user.ID)

	task, err := q.CreateDocumentTask(ctx, repository.CreateDocumentTaskParams{
		DocumentID: doc.ID, ModuleName: "analysis", OrganizationID: org.ID,
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, task.ID)
	assert.Equal(t, doc.ID, task.DocumentID)
	assert.Equal(t, "analysis", task.ModuleName)
	assert.Equal(t, "pending", task.Status)
}

func TestRepository_GetDocumentTask_OwnOrg(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)
	doc := createTestDocument(t, ctx, org.ID, user.ID)
	task := createTestTask(t, ctx, doc.ID, org.ID)

	fetched, err := q.GetDocumentTask(ctx, repository.GetDocumentTaskParams{
		ID: task.ID, OrganizationID: org.ID,
	})

	require.NoError(t, err)
	assert.Equal(t, task.ID, fetched.ID)
}

func TestRepository_GetDocumentTask_OtherOrg_NotFound(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org1 := createTestOrg(t, ctx)
	org2 := createTestOrg(t, ctx)
	user1 := createTestUser(t, ctx, org1.ID)
	doc := createTestDocument(t, ctx, org1.ID, user1.ID)
	task := createTestTask(t, ctx, doc.ID, org1.ID)

	// org2 пытается прочитать задачу org1
	_, err := q.GetDocumentTask(ctx, repository.GetDocumentTaskParams{
		ID: task.ID, OrganizationID: org2.ID,
	})
	require.Error(t, err, "must not return task belonging to another org")
}

func TestRepository_ListTasksByDocument_OwnOrg(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)
	doc := createTestDocument(t, ctx, org.ID, user.ID)

	for range 3 {
		createTestTask(t, ctx, doc.ID, org.ID)
	}

	tasks, err := q.ListTasksByDocument(ctx, repository.ListTasksByDocumentParams{
		DocumentID: doc.ID, OrganizationID: org.ID,
	})
	require.NoError(t, err)
	assert.Len(t, tasks, 3)
}

func TestRepository_ListTasksByDocument_OtherOrg_ReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org1 := createTestOrg(t, ctx)
	org2 := createTestOrg(t, ctx)
	user1 := createTestUser(t, ctx, org1.ID)
	doc := createTestDocument(t, ctx, org1.ID, user1.ID)
	createTestTask(t, ctx, doc.ID, org1.ID)

	// org2 перечисляет задачи документа org1
	tasks, err := q.ListTasksByDocument(ctx, repository.ListTasksByDocumentParams{
		DocumentID: doc.ID, OrganizationID: org2.ID,
	})
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestRepository_UpdateDocumentTaskStatus_OwnOrg(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)
	doc := createTestDocument(t, ctx, org.ID, user.ID)
	task := createTestTask(t, ctx, doc.ID, org.ID)

	updated, err := q.UpdateDocumentTaskStatus(ctx, repository.UpdateDocumentTaskStatusParams{
		ID: task.ID, OrganizationID: org.ID, Status: "processing",
	})

	require.NoError(t, err)
	assert.Equal(t, "processing", updated.Status)
}

func TestRepository_UpdateDocumentTaskStatus_OtherOrg_NotFound(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org1 := createTestOrg(t, ctx)
	org2 := createTestOrg(t, ctx)
	user1 := createTestUser(t, ctx, org1.ID)
	doc := createTestDocument(t, ctx, org1.ID, user1.ID)
	task := createTestTask(t, ctx, doc.ID, org1.ID)

	// org2 пытается обновить статус задачи org1
	_, err := q.UpdateDocumentTaskStatus(ctx, repository.UpdateDocumentTaskStatusParams{
		ID: task.ID, OrganizationID: org2.ID, Status: "processing",
	})
	require.Error(t, err, "must not update task belonging to another org")
}

func TestRepository_DeleteDocumentTask_OwnOrg(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org := createTestOrg(t, ctx)
	user := createTestUser(t, ctx, org.ID)
	doc := createTestDocument(t, ctx, org.ID, user.ID)
	task := createTestTask(t, ctx, doc.ID, org.ID)

	rows, err := q.DeleteDocumentTask(ctx, repository.DeleteDocumentTaskParams{
		ID: task.ID, OrganizationID: org.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows)
}

func TestRepository_DeleteDocumentTask_OtherOrg_DeletesNothing(t *testing.T) {
	ctx := context.Background()
	q := repository.New(testPool)
	org1 := createTestOrg(t, ctx)
	org2 := createTestOrg(t, ctx)
	user1 := createTestUser(t, ctx, org1.ID)
	doc := createTestDocument(t, ctx, org1.ID, user1.ID)
	task := createTestTask(t, ctx, doc.ID, org1.ID)

	// org2 пытается удалить задачу org1
	rows, err := q.DeleteDocumentTask(ctx, repository.DeleteDocumentTaskParams{
		ID: task.ID, OrganizationID: org2.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), rows, "must not delete task belonging to another org")
}
