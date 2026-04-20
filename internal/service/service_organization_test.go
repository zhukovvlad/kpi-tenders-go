//go:build !integration

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	storemock "go-kpi-tenders/internal/store/mock"
	"go-kpi-tenders/pkg/errs"
)

func newTestOrgService(ms *storemock.MockStore) *OrganizationService {
	return NewOrganizationService(ms, newTestLogger())
}

func baseRegisterParams() RegisterParams {
	return RegisterParams{
		OrgName:  "Acme Corp",
		Email:    "admin@acme.com",
		Password: "secret1234",
		FullName: "Admin User",
	}
}

// ── Register ─────────────────────────────────────────────────────────────────

func TestOrganizationService_Register_Success(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)

	orgID := uuid.New()
	userID := uuid.New()
	expectedOrg := repository.Organization{ID: orgID, Name: "Acme Corp"}
	expectedUser := repository.User{ID: userID, OrganizationID: orgID, Email: "admin@acme.com", Role: "admin"}

	ms.On("ExecTx", mock.Anything, mock.Anything).Return(nil)
	ms.On("CreateOrganization", mock.Anything, mock.Anything).Return(expectedOrg, nil)
	ms.On("CreateUser", mock.Anything, mock.Anything).Return(expectedUser, nil)

	svc := newTestOrgService(ms)
	org, user, err := svc.Register(ctx, baseRegisterParams())

	require.NoError(t, err)
	assert.Equal(t, "Acme Corp", org.Name)
	assert.Equal(t, "admin", user.Role)
	assert.Equal(t, orgID, user.OrganizationID)
	ms.AssertExpectations(t)
}

func TestOrganizationService_Register_INNConflict(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "organizations_inn_key"}
	ms.On("ExecTx", mock.Anything, mock.Anything).Return(nil)
	ms.On("CreateOrganization", mock.Anything, mock.Anything).Return(repository.Organization{}, pgErr)

	p := baseRegisterParams()
	p.INN = "1234567890"

	svc := newTestOrgService(ms)
	_, _, err := svc.Register(ctx, p)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeConflict, appErr.Code)
	assert.Contains(t, appErr.Message, "INN")
	ms.AssertExpectations(t)
}

func TestOrganizationService_Register_EmailConflict(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)

	expectedOrg := repository.Organization{ID: uuid.New(), Name: "Acme Corp"}
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "users_email_key"}

	ms.On("ExecTx", mock.Anything, mock.Anything).Return(nil)
	ms.On("CreateOrganization", mock.Anything, mock.Anything).Return(expectedOrg, nil)
	ms.On("CreateUser", mock.Anything, mock.Anything).Return(repository.User{}, pgErr)

	svc := newTestOrgService(ms)
	_, _, err := svc.Register(ctx, baseRegisterParams())

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeConflict, appErr.Code)
	assert.Contains(t, appErr.Message, "email")
	ms.AssertExpectations(t)
}

func TestOrganizationService_Register_InvalidINN(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore) // no expectations — ExecTx must NOT be called

	p := baseRegisterParams()
	p.INN = "not-10-digits"

	svc := newTestOrgService(ms)
	_, _, err := svc.Register(ctx, p)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	ms.AssertExpectations(t) // ExecTx not called
}

func TestOrganizationService_Register_ExecTxBeginFails(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)

	ms.On("ExecTx", mock.Anything, mock.Anything).Return(errors.New("db: connection refused"))

	svc := newTestOrgService(ms)
	_, _, err := svc.Register(ctx, baseRegisterParams())

	require.Error(t, err)
	ms.AssertExpectations(t)
}

// ── GetByID ──────────────────────────────────────────────────────────────────

func TestOrganizationService_GetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	id := uuid.New()

	ms.On("GetOrganizationByID", ctx, id).Return(repository.Organization{}, pgx.ErrNoRows)

	svc := newTestOrgService(ms)
	_, err := svc.GetByID(ctx, id)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	ms.AssertExpectations(t)
}

func TestOrganizationService_GetByID_Success(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	id := uuid.New()
	expected := repository.Organization{ID: id, Name: "Test Org"}

	ms.On("GetOrganizationByID", ctx, id).Return(expected, nil)

	svc := newTestOrgService(ms)
	org, err := svc.GetByID(ctx, id)

	require.NoError(t, err)
	assert.Equal(t, "Test Org", org.Name)
	ms.AssertExpectations(t)
}

// ── Delete ───────────────────────────────────────────────────────────────────

func TestOrganizationService_Delete_NotFound(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	id := uuid.New()

	ms.On("DeleteOrganization", ctx, id).Return(int64(0), nil)

	svc := newTestOrgService(ms)
	err := svc.Delete(ctx, id)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	ms.AssertExpectations(t)
}
