package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

const presignedURLTTL = 15 * time.Minute

// documentStorage is a consumer-side interface for the storage operations
// required by DocumentService. *storage.Client satisfies this interface.
type documentStorage interface {
	PresignedURLWithParams(ctx context.Context, storagePath string, ttl time.Duration, params url.Values) (string, error)
}

type DocumentService struct {
	repo    repository.Querier
	storage documentStorage // nil when S3 not configured
	log     *slog.Logger
}

func NewDocumentService(repo repository.Querier, storage documentStorage, log *slog.Logger) *DocumentService {
	return &DocumentService{repo: repo, storage: storage, log: log}
}

func (s *DocumentService) Create(ctx context.Context, params repository.CreateDocumentParams) (repository.Document, error) {
	doc, err := s.repo.CreateDocument(ctx, params)
	if err != nil {
		return repository.Document{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return doc, nil
}

func (s *DocumentService) Get(ctx context.Context, id, orgID uuid.UUID) (repository.Document, error) {
	doc, err := s.repo.GetDocument(ctx, repository.GetDocumentParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Document{}, errs.New(errs.CodeNotFound, "document not found", err)
		}
		return repository.Document{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return doc, nil
}

func (s *DocumentService) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]repository.Document, error) {
	docs, err := s.repo.ListRootDocumentsByOrganization(ctx, orgID)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return docs, nil
}

func (s *DocumentService) ListBySite(ctx context.Context, orgID, siteID uuid.UUID) ([]repository.Document, error) {
	docs, err := s.repo.ListRootDocumentsBySite(ctx, repository.ListRootDocumentsBySiteParams{
		OrganizationID: orgID,
		SiteID:         pgtype.UUID{Bytes: siteID, Valid: true},
	})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return docs, nil
}

func (s *DocumentService) ListByParent(ctx context.Context, parentID, orgID uuid.UUID) ([]repository.Document, error) {
	docs, err := s.repo.ListDocumentsByParent(ctx, repository.ListDocumentsByParentParams{
		ParentID:       pgtype.UUID{Bytes: parentID, Valid: true},
		OrganizationID: orgID,
	})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return docs, nil
}

func (s *DocumentService) Delete(ctx context.Context, id, orgID uuid.UUID) error {
	rows, err := s.repo.DeleteDocument(ctx, repository.DeleteDocumentParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "document not found", nil)
	}
	return nil
}

// GetPresignedURL generates a time-limited presigned GET URL for the document.
// It enforces org-level isolation: documents belonging to a different org return
// 404 (not 403) to avoid leaking document existence across org boundaries.
// When download is true the URL includes a Content-Disposition: attachment header
// so the browser downloads the file instead of opening it inline.
func (s *DocumentService) GetPresignedURL(ctx context.Context, docID, orgID uuid.UUID, download bool) (string, error) {
	if s.storage == nil {
		return "", errs.New(errs.CodeInternalError, "storage unavailable", nil)
	}

	doc, err := s.repo.GetDocumentByID(ctx, docID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errs.New(errs.CodeNotFound, "document not found", err)
		}
		return "", errs.New(errs.CodeInternalError, "internal server error", err)
	}

	if doc.OrganizationID != orgID {
		return "", errs.New(errs.CodeNotFound, "document not found", nil)
	}

	var reqParams url.Values
	if download {
		// Sanitize filename: remove characters that could break the
		// Content-Disposition header value (quotes, CR, LF).
		safeName := strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(doc.FileName)
		reqParams = url.Values{
			"response-content-disposition": []string{
				fmt.Sprintf(`attachment; filename="%s"`, safeName),
			},
		}
	}

	presignedURL, err := s.storage.PresignedURLWithParams(ctx, doc.StoragePath, presignedURLTTL, reqParams)
	if err != nil {
		s.log.Error("storage: presign error", "doc_id", docID, "err", err)
		return "", errs.New(errs.CodeInternalError, "storage error", err)
	}

	return presignedURL, nil
}
func (s *DocumentService) UpdateMeta(ctx context.Context, id, orgID uuid.UUID, contractKindID, fileRoleID, bundleID *uuid.UUID) (repository.Document, error) {
	toUUID := func(u *uuid.UUID) pgtype.UUID {
		if u == nil {
			return pgtype.UUID{}
		}
		return pgtype.UUID{Bytes: *u, Valid: true}
	}
	doc, err := s.repo.UpdateDocumentMeta(ctx, repository.UpdateDocumentMetaParams{
		ID:             id,
		OrganizationID: orgID,
		ContractKindID: toUUID(contractKindID),
		FileRoleID:     toUUID(fileRoleID),
		BundleID:       toUUID(bundleID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Document{}, errs.New(errs.CodeNotFound, "document not found", err)
		}
		s.log.Error("update document meta failed", "err", err, "id", id, "org_id", orgID)
		return repository.Document{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return doc, nil
}
