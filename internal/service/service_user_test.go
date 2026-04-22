package service

import (
	"context"
	"errors"
	"strings"
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

func newTestUserService(mq *storemock.MockQuerier) *UserService {
	return NewUserService(mq, newTestLogger())
}

func baseCreateUserParams(orgID uuid.UUID) CreateUserParams {
	return CreateUserParams{
		OrgID:    orgID,
		Email:    "member@acme.com",
		Password: "secret1234",
		FullName: "John Doe",
		Role:     "member",
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestUserService_Create_Success(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	orgID := uuid.New()
	userID := uuid.New()
	expected := repository.User{ID: userID, OrganizationID: orgID, Email: "member@acme.com", Role: "member"}

	mq.On("CreateUser", mock.Anything, mock.MatchedBy(func(p repository.CreateUserParams) bool {
		return p.Email == "member@acme.com" && p.Role == "member" && p.OrganizationID == orgID
	})).Return(expected, nil)

	svc := newTestUserService(mq)
	user, err := svc.Create(ctx, baseCreateUserParams(orgID))

	require.NoError(t, err)
	assert.Equal(t, "member@acme.com", user.Email)
	assert.Equal(t, "member", user.Role)
	mq.AssertExpectations(t)
}

func TestUserService_Create_AdminRole_Success(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	orgID := uuid.New()
	expected := repository.User{OrganizationID: orgID, Email: "admin2@acme.com", Role: "admin"}

	mq.On("CreateUser", mock.Anything, mock.MatchedBy(func(p repository.CreateUserParams) bool {
		return p.Role == "admin"
	})).Return(expected, nil)

	p := baseCreateUserParams(orgID)
	p.Role = "admin"
	p.Email = "admin2@acme.com"

	svc := newTestUserService(mq)
	user, err := svc.Create(ctx, p)

	require.NoError(t, err)
	assert.Equal(t, "admin", user.Role)
	mq.AssertExpectations(t)
}

func TestUserService_Create_InvalidRole_ReturnsValidationFailed(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier) // no expectations — CreateUser must NOT be called

	p := baseCreateUserParams(uuid.New())
	p.Role = "superuser"

	svc := newTestUserService(mq)
	_, err := svc.Create(ctx, p)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	mq.AssertExpectations(t)
}

func TestUserService_Create_EmailConflict_ReturnsConflict(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "users_email_key"}
	mq.On("CreateUser", mock.Anything, mock.Anything).Return(repository.User{}, pgErr)

	svc := newTestUserService(mq)
	_, err := svc.Create(ctx, baseCreateUserParams(uuid.New()))

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeConflict, appErr.Code)
	assert.Contains(t, appErr.Message, "email")
	mq.AssertExpectations(t)
}

func TestUserService_Create_PasswordTooLong_ReturnsValidationFailed(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier) // no expectations — DB must NOT be called

	p := baseCreateUserParams(uuid.New())
	p.Password = strings.Repeat("a", 73) // bcrypt limit is 72 bytes

	svc := newTestUserService(mq)
	_, err := svc.Create(ctx, p)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	mq.AssertExpectations(t)
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestUserService_List_Success(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	orgID := uuid.New()
	rows := []repository.ListUsersByOrganizationRow{
		{ID: uuid.New(), OrganizationID: orgID, Email: "a@acme.com"},
		{ID: uuid.New(), OrganizationID: orgID, Email: "b@acme.com"},
	}

	mq.On("ListUsersByOrganization", mock.Anything, orgID).Return(rows, nil)

	svc := newTestUserService(mq)
	users, err := svc.List(ctx, orgID)

	require.NoError(t, err)
	assert.Len(t, users, 2)
	mq.AssertExpectations(t)
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestUserService_Update_Role_Success(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	orgID := uuid.New()
	userID := uuid.New()
	newRole := "admin"
	expected := repository.UpdateUserRow{ID: userID, OrganizationID: orgID, Role: "admin"}

	mq.On("UpdateUser", mock.Anything, mock.MatchedBy(func(p repository.UpdateUserParams) bool {
		return p.ID == userID && p.OrganizationID == orgID && p.Role.String == "admin"
	})).Return(expected, nil)

	svc := newTestUserService(mq)
	updated, err := svc.Update(ctx, UpdateUserParams{UserID: userID, OrgID: orgID, Role: &newRole})

	require.NoError(t, err)
	assert.Equal(t, "admin", updated.Role)
	mq.AssertExpectations(t)
}

func TestUserService_Update_NoFields_ReturnsValidationFailed(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier) // no expectations — should not reach DB

	svc := newTestUserService(mq)
	_, err := svc.Update(ctx, UpdateUserParams{UserID: uuid.New(), OrgID: uuid.New()})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	mq.AssertExpectations(t)
}

func TestUserService_Update_InvalidRole_ReturnsValidationFailed(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier) // no expectations

	badRole := "owner"
	svc := newTestUserService(mq)
	_, err := svc.Update(ctx, UpdateUserParams{UserID: uuid.New(), OrgID: uuid.New(), Role: &badRole})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	mq.AssertExpectations(t)
}

func TestUserService_Update_UserNotFound_ReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	mq.On("UpdateUser", mock.Anything, mock.Anything).Return(repository.UpdateUserRow{}, pgx.ErrNoRows)

	newRole := "member"
	svc := newTestUserService(mq)
	_, err := svc.Update(ctx, UpdateUserParams{UserID: uuid.New(), OrgID: uuid.New(), Role: &newRole})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	mq.AssertExpectations(t)
}

// ── Deactivate ────────────────────────────────────────────────────────────────

func TestUserService_Deactivate_Success(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	orgID := uuid.New()
	userID := uuid.New()
	expected := repository.UpdateUserRow{ID: userID, IsActive: false}

	mq.On("UpdateUser", mock.Anything, mock.MatchedBy(func(p repository.UpdateUserParams) bool {
		return p.ID == userID && p.OrganizationID == orgID && p.IsActive.Valid && !p.IsActive.Bool
	})).Return(expected, nil)

	svc := newTestUserService(mq)
	updated, err := svc.Deactivate(ctx, userID, orgID)

	require.NoError(t, err)
	assert.False(t, updated.IsActive)
	mq.AssertExpectations(t)
}

func TestUserService_Deactivate_UserNotFound_ReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	mq.On("UpdateUser", mock.Anything, mock.Anything).Return(repository.UpdateUserRow{}, pgx.ErrNoRows)

	svc := newTestUserService(mq)
	_, err := svc.Deactivate(ctx, uuid.New(), uuid.New())

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	mq.AssertExpectations(t)
}

// ── GetProfile ───────────────────────────────────────────────────────────────

func TestUserService_GetProfile_Success(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	userID := uuid.New()
	orgID := uuid.New()
	expected := repository.GetUserByIDAndOrgRow{
		ID:             userID,
		OrganizationID: orgID,
		Email:          "user@acme.com",
		FullName:       "John Doe",
		Role:           "member",
		IsActive:       true,
	}

	mq.On("GetUserByIDAndOrg", mock.Anything, repository.GetUserByIDAndOrgParams{
		ID:             userID,
		OrganizationID: orgID,
	}).Return(expected, nil)

	svc := newTestUserService(mq)
	user, err := svc.GetProfile(ctx, userID, orgID)

	require.NoError(t, err)
	assert.Equal(t, userID, user.ID)
	assert.Equal(t, orgID, user.OrganizationID)
	assert.Equal(t, "user@acme.com", user.Email)
	assert.Equal(t, "member", user.Role)
	assert.True(t, user.IsActive)
	mq.AssertExpectations(t)
}

func TestUserService_GetProfile_NotFound_ReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	mq.On("GetUserByIDAndOrg", mock.Anything, mock.Anything).Return(repository.GetUserByIDAndOrgRow{}, pgx.ErrNoRows)

	svc := newTestUserService(mq)
	_, err := svc.GetProfile(ctx, uuid.New(), uuid.New())

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	mq.AssertExpectations(t)
}

func TestUserService_GetProfile_InactiveUser_ReturnsUnauthorized(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	userID := uuid.New()
	orgID := uuid.New()

	mq.On("GetUserByIDAndOrg", mock.Anything, mock.Anything).Return(
		repository.GetUserByIDAndOrgRow{ID: userID, IsActive: false}, nil,
	)

	svc := newTestUserService(mq)
	_, err := svc.GetProfile(ctx, userID, orgID)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeUnauthorized, appErr.Code)
	assert.Equal(t, "account is unavailable", appErr.Message)
	mq.AssertExpectations(t)
}

func TestUserService_GetProfile_DBError_ReturnsInternalError(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	mq.On("GetUserByIDAndOrg", mock.Anything, mock.Anything).Return(
		repository.GetUserByIDAndOrgRow{}, errors.New("db: connection refused"),
	)

	svc := newTestUserService(mq)
	_, err := svc.GetProfile(ctx, uuid.New(), uuid.New())

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeInternalError, appErr.Code)
	mq.AssertExpectations(t)
}
