package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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

// SiteEvent is the frontend-facing representation of a site audit log event.
type SiteEvent struct {
	ID         uuid.UUID `json:"id"`
	SiteID     uuid.UUID `json:"site_id"`
	Kind       string    `json:"kind"`
	ActorName  string    `json:"actor_name"`
	Message    string    `json:"message"`
	OccurredAt string    `json:"occurred_at"`
}

// ListEventsBySite returns site events mapped to the SiteEvent DTO.
func (s *SiteAuditService) ListEventsBySite(ctx context.Context, siteID, orgID uuid.UUID, limit, offset int32) ([]SiteEvent, error) {
	rows, err := s.repo.ListSiteEventsBySite(ctx, repository.ListSiteEventsBySiteParams{
		SiteID:         siteID,
		OrganizationID: orgID,
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		s.log.Error("list site events failed", "err", err, "site_id", siteID, "org_id", orgID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	result := make([]SiteEvent, len(rows))
	for i, r := range rows {
		actorName := fmt.Sprintf("%v", r.ActorName)
		result[i] = SiteEvent{
			ID:         r.ID,
			SiteID:     r.SiteID,
			Kind:       r.EventType,
			ActorName:  actorName,
			Message:    buildEventMessage(r.EventType, r.Payload),
			OccurredAt: r.CreatedAt.Format(time.RFC3339),
		}
	}
	return result, nil
}

// buildEventMessage generates a human-readable message from event_type and JSON payload.
func buildEventMessage(eventType string, payload json.RawMessage) string {
	var p map[string]interface{}
	_ = json.Unmarshal(payload, &p)

	switch eventType {
	case "site_created":
		return "Объект строительства создан"
	case "metadata_updated":
		return "Метаданные объекта обновлены"
	case "document_uploaded":
		if name, ok := p["document_name"].(string); ok && name != "" {
			return fmt.Sprintf("Документ загружен: %s", name)
		}
		return "Документ загружен"
	case "document_deleted":
		if name, ok := p["document_name"].(string); ok && name != "" {
			return fmt.Sprintf("Документ удалён: %s", name)
		}
		return "Документ удалён"
	case "extraction_started":
		return "Запущено извлечение параметров"
	case "extraction_completed":
		return "Извлечение параметров завершено"
	case "extraction_failed":
		if msg, ok := p["error"].(string); ok && msg != "" {
			return fmt.Sprintf("Ошибка извлечения: %s", msg)
		}
		return "Ошибка извлечения параметров"
	default:
		return eventType
	}
}
