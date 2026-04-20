//go:build !integration

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/store/mock"
	"go-kpi-tenders/pkg/errs"
)

const (
	testAccessSecret  = "test-access-secret-must-be-32chars!"
	testRefreshSecret = "test-refresh-secret-must-be-32chars"
)

func newTestAuthService(ms *mock.MockStore) *AuthService {
	return NewAuthService(ms, newTestLogger(), testAccessSecret, testRefreshSecret)
}

func TestAuthService_Login_Success(t *testing.T) {
	ctx := context.Background()
	ms := new(mock.MockStore)

	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	require.NoError(t, err)

	expectedUser := repository.User{
		ID:             uuid.New(),
		OrganizationID: uuid.New(),
		Email:          "user@example.com",
		PasswordHash:   string(hash),
		Role:           "admin",
	}
	ms.On("GetUserByEmail", ctx, "user@example.com").Return(expectedUser, nil)

	svc := newTestAuthService(ms)
	access, refresh, err := svc.Login(ctx, "user@example.com", "password123")

	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, refresh)
	ms.AssertExpectations(t)
}

func TestAuthService_Login_UserNotFound_ReturnsUnauthorized(t *testing.T) {
	ctx := context.Background()
	ms := new(mock.MockStore)

	ms.On("GetUserByEmail", ctx, "ghost@example.com").Return(repository.User{}, pgx.ErrNoRows)

	svc := newTestAuthService(ms)
	_, _, err := svc.Login(ctx, "ghost@example.com", "password123")

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeUnauthorized, appErr.Code)
	assert.Equal(t, "invalid email or password", appErr.Message)
	ms.AssertExpectations(t)
}

// TestAuthService_Login_TimingEqualization verifies that missing-user and
// wrong-password responses are indistinguishable: same error code and message.
// The dummyHash bcrypt run is what equalises the latency; here we verify the
// external contract (identical error shape) rather than timing directly.
func TestAuthService_Login_TimingEqualization(t *testing.T) {
	ctx := context.Background()

	hashForExistingUser, _ := bcrypt.GenerateFromPassword([]byte("correct"), bcrypt.MinCost)
	existingUser := repository.User{
		ID:           uuid.New(),
		Email:        "user@example.com",
		PasswordHash: string(hashForExistingUser),
	}

	for _, tc := range []struct {
		name  string
		email string
		pass  string
		setup func(ms *mock.MockStore)
	}{
		{
			name:  "missing user",
			email: "ghost@example.com",
			pass:  "any",
			setup: func(ms *mock.MockStore) {
				ms.On("GetUserByEmail", ctx, "ghost@example.com").Return(repository.User{}, pgx.ErrNoRows)
			},
		},
		{
			name:  "wrong password",
			email: "user@example.com",
			pass:  "wrong",
			setup: func(ms *mock.MockStore) {
				ms.On("GetUserByEmail", ctx, "user@example.com").Return(existingUser, nil)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms := new(mock.MockStore)
			tc.setup(ms)

			svc := newTestAuthService(ms)
			_, _, err := svc.Login(ctx, tc.email, tc.pass)

			var appErr *errs.Error
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, errs.CodeUnauthorized, appErr.Code)
			assert.Equal(t, "invalid email or password", appErr.Message)
			ms.AssertExpectations(t)
		})
	}
}

func TestAuthService_Login_RepositoryError_ReturnsInternalError(t *testing.T) {
	ctx := context.Background()
	ms := new(mock.MockStore)

	ms.On("GetUserByEmail", ctx, "user@example.com").Return(repository.User{}, errors.New("db: connection refused"))

	svc := newTestAuthService(ms)
	_, _, err := svc.Login(ctx, "user@example.com", "pass")

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeInternalError, appErr.Code)
	ms.AssertExpectations(t)
}

func TestAuthService_ValidateAccessToken_RoundTrip(t *testing.T) {
	svc := newTestAuthService(new(mock.MockStore))
	userID := uuid.New()
	orgID := uuid.New()

	access, _, err := svc.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	claims, err := svc.ValidateAccessToken(access)
	require.NoError(t, err)
	assert.Equal(t, userID, claims.UserID)
	assert.Equal(t, orgID, claims.OrgID)
	assert.Equal(t, "admin", claims.Role)
}

func TestAuthService_ValidateAccessToken_InvalidSignature(t *testing.T) {
	svc := newTestAuthService(new(mock.MockStore))

	_, err := svc.ValidateAccessToken("invalid.jwt.token")
	require.Error(t, err)

	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeUnauthorized, appErr.Code)
}

func TestAuthService_ValidateRefreshToken_RoundTrip(t *testing.T) {
	svc := newTestAuthService(new(mock.MockStore))

	_, refresh, err := svc.GenerateTokens(uuid.New(), uuid.New(), "member")
	require.NoError(t, err)

	claims, err := svc.ValidateRefreshToken(refresh)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, claims.UserID)
}
