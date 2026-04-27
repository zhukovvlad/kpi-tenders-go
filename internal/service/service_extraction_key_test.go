package service

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestNormalizeExtractionKeyName_Cyrillic(t *testing.T) {
	assert.Equal(t, "kakoy_protsent_avansa", normalizeExtractionKeyName(" Какой процент аванса? "))
	assert.Equal(t, "dogovor_2026", normalizeExtractionKeyName("Договор №2026"))
}

func TestNormalizeExtractionKeyName_ASCIIOnlyFallback(t *testing.T) {
	key := normalizeExtractionKeyName("总金额 " + strings.Repeat("界", 120) + " total_amount_m2")

	assert.Equal(t, "total_amount_m2", key)
	assert.True(t, utf8.ValidString(key))
	assert.LessOrEqual(t, len(key), 80)
}

func TestExtractionKeyService_Resolve_EmptySourceQuery(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewExtractionKeyService(mq, newTestLogger())

	_, _, err := svc.Resolve(context.Background(), ResolveExtractionKeyParams{
		OrganizationID: uuid.New(),
		SourceQuery:    "   ",
	})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	mq.AssertExpectations(t)
}

func TestExtractionKeyService_Resolve_DuplicateBySourceQuery(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewExtractionKeyService(mq, newTestLogger())

	orgID := uuid.New()
	expected := repository.ExtractionKey{
		ID:             uuid.New(),
		OrganizationID: orgID,
		KeyName:        "advance_payment_percent",
		SourceQuery:    "Какой процент аванса?",
		DataType:       "number",
	}

	mq.On("GetExtractionKeyByOrgAndSourceQuery", mock.Anything, repository.GetExtractionKeyByOrgAndSourceQueryParams{
		OrganizationID: orgID,
		SourceQuery:    "Какой процент аванса?",
	}).Return(expected, nil)

	key, duplicate, err := svc.Resolve(context.Background(), ResolveExtractionKeyParams{
		OrganizationID: orgID,
		SourceQuery:    "  Какой процент аванса?  ",
	})

	require.NoError(t, err)
	assert.True(t, duplicate)
	assert.Equal(t, expected, key)
	mq.AssertExpectations(t)
	mq.AssertNotCalled(t, "CreateExtractionKey")
}

func TestExtractionKeyService_Resolve_DuplicateByKeyName(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewExtractionKeyService(mq, newTestLogger())

	orgID := uuid.New()
	expected := repository.ExtractionKey{
		ID:             uuid.New(),
		OrganizationID: orgID,
		KeyName:        "kakoy_protsent_avansa",
		SourceQuery:    "Какой % аванса?",
		DataType:       "number",
	}

	mq.On("GetExtractionKeyByOrgAndSourceQuery", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgx.ErrNoRows)
	mq.On("GetExtractionKeyByOrgAndKeyName", mock.Anything, repository.GetExtractionKeyByOrgAndKeyNameParams{
		OrganizationID: orgID,
		KeyName:        "kakoy_protsent_avansa",
	}).Return(expected, nil)

	key, duplicate, err := svc.Resolve(context.Background(), ResolveExtractionKeyParams{
		OrganizationID: orgID,
		SourceQuery:    "Какой процент аванса?",
	})

	require.NoError(t, err)
	assert.True(t, duplicate)
	assert.Equal(t, expected, key)
	mq.AssertExpectations(t)
	mq.AssertNotCalled(t, "CreateExtractionKey")
}

func TestExtractionKeyService_Resolve_CreatesNewKey(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewExtractionKeyService(mq, newTestLogger())

	orgID := uuid.New()
	expected := repository.ExtractionKey{
		ID:             uuid.New(),
		OrganizationID: orgID,
		KeyName:        "kakoy_protsent_avansa",
		SourceQuery:    "Какой процент аванса?",
		Description:    pgtype.Text{String: "Какой процент аванса?", Valid: true},
		DataType:       "number",
	}

	mq.On("GetExtractionKeyByOrgAndSourceQuery", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgx.ErrNoRows)
	mq.On("GetExtractionKeyByOrgAndKeyName", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgx.ErrNoRows)
	mq.On("CreateExtractionKey", mock.Anything, mock.MatchedBy(func(p repository.CreateExtractionKeyParams) bool {
		return p.OrganizationID == orgID &&
			p.KeyName == "kakoy_protsent_avansa" &&
			p.SourceQuery == "Какой процент аванса?" &&
			p.Description.Valid &&
			p.Description.String == "Какой процент аванса?" &&
			p.DataType == "number" &&
			!p.IsRequired
	})).Return(expected, nil)

	key, duplicate, err := svc.Resolve(context.Background(), ResolveExtractionKeyParams{
		OrganizationID: orgID,
		SourceQuery:    "Какой процент аванса?",
	})

	require.NoError(t, err)
	assert.False(t, duplicate)
	assert.Equal(t, expected, key)
	mq.AssertExpectations(t)
}

func TestExtractionKeyService_Resolve_UniqueRaceReadsExistingKey(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewExtractionKeyService(mq, newTestLogger())

	orgID := uuid.New()
	expected := repository.ExtractionKey{
		ID:             uuid.New(),
		OrganizationID: orgID,
		KeyName:        "kakoy_protsent_avansa",
		SourceQuery:    "Какой процент аванса?",
		DataType:       "number",
	}
	uniqueErr := &pgconn.PgError{Code: "23505", ConstraintName: "uq_extraction_keys_org_key"}

	mq.On("GetExtractionKeyByOrgAndSourceQuery", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgx.ErrNoRows)
	mq.On("GetExtractionKeyByOrgAndKeyName", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgx.ErrNoRows).Once()
	mq.On("CreateExtractionKey", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, uniqueErr)
	mq.On("GetExtractionKeyByOrgAndKeyName", mock.Anything, repository.GetExtractionKeyByOrgAndKeyNameParams{
		OrganizationID: orgID,
		KeyName:        "kakoy_protsent_avansa",
	}).Return(expected, nil).Once()

	key, duplicate, err := svc.Resolve(context.Background(), ResolveExtractionKeyParams{
		OrganizationID: orgID,
		SourceQuery:    "Какой процент аванса?",
	})

	require.NoError(t, err)
	assert.True(t, duplicate)
	assert.Equal(t, expected, key)
	mq.AssertExpectations(t)
}
