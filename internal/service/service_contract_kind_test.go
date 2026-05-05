package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	storemock "go-kpi-tenders/internal/store/mock"
	"go-kpi-tenders/pkg/errs"
)

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// ── List ──────────────────────────────────────────────────────────────────────

func TestContractKindService_List_Success(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	orgID := uuid.New()
	expected := []repository.DocumentContractKind{{ID: uuid.New(), DisplayName: "Генподряд"}}
	mq.On("ListContractKindsByOrg", mock.Anything, pgUUID(orgID)).Return(expected, nil)

	kinds, err := svc.List(context.Background(), orgID)

	require.NoError(t, err)
	assert.Equal(t, expected, kinds)
	mq.AssertExpectations(t)
}

func TestContractKindService_List_DBError_Returns500(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	mq.On("ListContractKindsByOrg", mock.Anything, mock.Anything).Return(nil, errors.New("db down"))

	_, err := svc.List(context.Background(), uuid.New())

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeInternalError, appErr.Code)
	mq.AssertExpectations(t)
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestContractKindService_Get_Success(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	id, orgID := uuid.New(), uuid.New()
	expected := repository.DocumentContractKind{ID: id, DisplayName: "Стройконтроль"}
	mq.On("GetContractKind", mock.Anything, repository.GetContractKindParams{
		ID:             id,
		OrganizationID: pgUUID(orgID),
	}).Return(expected, nil)

	kind, err := svc.Get(context.Background(), id, orgID)

	require.NoError(t, err)
	assert.Equal(t, expected, kind)
	mq.AssertExpectations(t)
}

func TestContractKindService_Get_NotFound_Returns404(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	mq.On("GetContractKind", mock.Anything, mock.Anything).Return(repository.DocumentContractKind{}, pgx.ErrNoRows)

	_, err := svc.Get(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	mq.AssertExpectations(t)
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestContractKindService_Create_Success(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	orgID := uuid.New()
	expected := repository.DocumentContractKind{ID: uuid.New(), DisplayName: "Генподряд"}
	mq.On("CreateContractKind", mock.Anything, mock.Anything).Return(expected, nil)

	kind, err := svc.Create(context.Background(), orgID, "Генподряд", 0, true)

	require.NoError(t, err)
	assert.Equal(t, expected, kind)
	mq.AssertExpectations(t)
}

func TestContractKindService_Create_UniqueViolation_Returns409(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "uq_contract_kinds_org_name"}
	mq.On("CreateContractKind", mock.Anything, mock.Anything).Return(repository.DocumentContractKind{}, pgErr)

	_, err := svc.Create(context.Background(), uuid.New(), "Генподряд", 0, true)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeConflict, appErr.Code)
	mq.AssertExpectations(t)
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestContractKindService_Update_Success(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	id, orgID := uuid.New(), uuid.New()
	expected := repository.DocumentContractKind{ID: id, DisplayName: "Обновлён"}
	mq.On("UpdateContractKind", mock.Anything, mock.Anything).Return(expected, nil)

	kind, err := svc.Update(context.Background(), id, orgID, "Обновлён", 1, false)

	require.NoError(t, err)
	assert.Equal(t, expected, kind)
	mq.AssertExpectations(t)
}

func TestContractKindService_Update_NotFound_Returns404(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	mq.On("UpdateContractKind", mock.Anything, mock.Anything).Return(repository.DocumentContractKind{}, pgx.ErrNoRows)

	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), "x", 0, true)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	mq.AssertExpectations(t)
}

func TestContractKindService_Update_UniqueViolation_Returns409(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "uq_contract_kinds_org_name"}
	mq.On("UpdateContractKind", mock.Anything, mock.Anything).Return(repository.DocumentContractKind{}, pgErr)

	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), "Генподряд", 0, true)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeConflict, appErr.Code)
	mq.AssertExpectations(t)
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestContractKindService_Delete_Success(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	mq.On("DeleteContractKind", mock.Anything, mock.Anything).Return(int64(1), nil)

	err := svc.Delete(context.Background(), uuid.New(), uuid.New())

	require.NoError(t, err)
	mq.AssertExpectations(t)
}

func TestContractKindService_Delete_NotFound_Returns404(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewContractKindService(mq, newTestLogger())

	mq.On("DeleteContractKind", mock.Anything, mock.Anything).Return(int64(0), nil)

	err := svc.Delete(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	mq.AssertExpectations(t)
}
