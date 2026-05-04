package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type SiteAuditService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewSiteAuditService(repo repository.Querier, log *slog.Logger) *SiteAuditService {
	return &SiteAuditService{repo: repo, log: log}
}

// Log writes an audit event for a construction site.
// actorUserID may be uuid.Nil for system events (watchdog, worker callbacks).
func (s *SiteAuditService) Log(ctx context.Context, orgID, siteID, actorUserID uuid.UUID, eventType string, payload []byte) (repository.SiteAuditLog, error) {
	actorPG := pgtype.UUID{}
	if actorUserID != uuid.Nil {
		actorPG = pgtype.UUID{Bytes: actorUserID, Valid: true}
	}

	if payload == nil {
		payload = []byte("{}")
	}

	event, err := s.repo.CreateSiteAuditEvent(ctx, repository.CreateSiteAuditEventParams{
		OrganizationID: orgID,
		SiteID:         siteID,
		ActorUserID:    actorPG,
		EventType:      eventType,
		Payload:        payload,
	})
	if err != nil {
		s.log.Error("create site audit event failed", "err", err, "site_id", siteID, "event_type", eventType)
		return repository.SiteAuditLog{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return event, nil
}

func (s *SiteAuditService) ListBySite(ctx context.Context, siteID, orgID uuid.UUID, limit, offset int32) ([]repository.SiteAuditLog, error) {
	events, err := s.repo.ListSiteAuditLogBySite(ctx, repository.ListSiteAuditLogBySiteParams{
		SiteID:         siteID,
		OrganizationID: orgID,
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		s.log.Error("list site audit log failed", "err", err, "site_id", siteID, "org_id", orgID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return events, nil
}
