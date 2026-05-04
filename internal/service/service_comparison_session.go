package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type ComparisonSessionService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewComparisonSessionService(repo repository.Querier, log *slog.Logger) *ComparisonSessionService {
	return &ComparisonSessionService{repo: repo, log: log}
}

func (s *ComparisonSessionService) Create(ctx context.Context, orgID, createdBy uuid.UUID, name *string, contractKindID *uuid.UUID) (repository.ComparisonSession, error) {
	params := repository.CreateComparisonSessionParams{
		OrganizationID: orgID,
		CreatedBy:      pgtype.UUID{Bytes: createdBy, Valid: true},
	}
	if name != nil {
		params.Name = pgtype.Text{String: *name, Valid: true}
	}
	if contractKindID != nil {
		params.ContractKindID = pgtype.UUID{Bytes: *contractKindID, Valid: true}
	}

	session, err := s.repo.CreateComparisonSession(ctx, params)
	if err != nil {
		s.log.Error("create comparison session failed", "err", err, "org_id", orgID)
		return repository.ComparisonSession{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return session, nil
}

func (s *ComparisonSessionService) Get(ctx context.Context, id, orgID uuid.UUID) (repository.ComparisonSession, error) {
	session, err := s.repo.GetComparisonSession(ctx, repository.GetComparisonSessionParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ComparisonSession{}, errs.New(errs.CodeNotFound, "comparison session not found", err)
		}
		s.log.Error("get comparison session failed", "err", err, "id", id, "org_id", orgID)
		return repository.ComparisonSession{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return session, nil
}

func (s *ComparisonSessionService) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]repository.ComparisonSession, error) {
	sessions, err := s.repo.ListComparisonSessionsByOrg(ctx, orgID)
	if err != nil {
		s.log.Error("list comparison sessions failed", "err", err, "org_id", orgID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return sessions, nil
}

func (s *ComparisonSessionService) Delete(ctx context.Context, id, orgID uuid.UUID) error {
	rows, err := s.repo.DeleteComparisonSession(ctx, repository.DeleteComparisonSessionParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		s.log.Error("delete comparison session failed", "err", err, "id", id, "org_id", orgID)
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "comparison session not found", nil)
	}
	return nil
}

func (s *ComparisonSessionService) AddDocument(ctx context.Context, sessionID, documentID, orgID uuid.UUID, position int16) (repository.ComparisonSessionDocument, error) {
	doc, err := s.repo.AddDocumentToComparisonSession(ctx, repository.AddDocumentToComparisonSessionParams{
		SessionID:      sessionID,
		DocumentID:     documentID,
		OrganizationID: orgID,
		Position:       position,
	})
	if err != nil {
		s.log.Error("add document to comparison session failed", "err", err, "session_id", sessionID, "document_id", documentID)
		return repository.ComparisonSessionDocument{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return doc, nil
}

func (s *ComparisonSessionService) RemoveDocument(ctx context.Context, sessionID, documentID uuid.UUID) error {
	rows, err := s.repo.RemoveDocumentFromComparisonSession(ctx, repository.RemoveDocumentFromComparisonSessionParams{
		SessionID:  sessionID,
		DocumentID: documentID,
	})
	if err != nil {
		s.log.Error("remove document from comparison session failed", "err", err, "session_id", sessionID, "document_id", documentID)
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "document not found in comparison session", nil)
	}
	return nil
}

func (s *ComparisonSessionService) ListDocuments(ctx context.Context, sessionID uuid.UUID) ([]repository.ComparisonSessionDocument, error) {
	docs, err := s.repo.ListComparisonSessionDocuments(ctx, sessionID)
	if err != nil {
		s.log.Error("list comparison session documents failed", "err", err, "session_id", sessionID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return docs, nil
}
