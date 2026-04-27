package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/pgutil"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

// ExtractionKeyService owns tenant-scoped extraction-key resolution and the
// compact worker payload shape derived from stored keys.
type ExtractionKeyService struct {
	repo repository.Querier
	log  *slog.Logger
}

// NewExtractionKeyService constructs an extraction-key service backed by the
// provided repository interface.
func NewExtractionKeyService(repo repository.Querier, log *slog.Logger) *ExtractionKeyService {
	return &ExtractionKeyService{repo: repo, log: log}
}

// ResolveExtractionKeyParams contains the tenant and original user question
// needed to resolve or create an extraction key.
type ResolveExtractionKeyParams struct {
	OrganizationID uuid.UUID
	SourceQuery    string
}

// Resolve deduplicates a natural-language question inside one organization and
// returns the existing key when either the original query or the normalized
// key_name already exists. The bool result is true for duplicates.
func (s *ExtractionKeyService) Resolve(ctx context.Context, params ResolveExtractionKeyParams) (repository.ExtractionKey, bool, error) {
	sourceQuery := strings.TrimSpace(params.SourceQuery)
	if sourceQuery == "" {
		return repository.ExtractionKey{}, false, errs.New(errs.CodeValidationFailed, "source_query is required", nil)
	}

	existing, err := s.repo.GetExtractionKeyByOrgAndSourceQuery(ctx, repository.GetExtractionKeyByOrgAndSourceQueryParams{
		OrganizationID: params.OrganizationID,
		SourceQuery:    sourceQuery,
	})
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return repository.ExtractionKey{}, false, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	keyName := normalizeExtractionKeyName(sourceQuery)
	if keyName == "" {
		return repository.ExtractionKey{}, false, errs.New(errs.CodeValidationFailed, "source_query must contain ASCII letters, digits, or supported Cyrillic text", nil)
	}

	existing, err = s.repo.GetExtractionKeyByOrgAndKeyName(ctx, repository.GetExtractionKeyByOrgAndKeyNameParams{
		OrganizationID: params.OrganizationID,
		KeyName:        keyName,
	})
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return repository.ExtractionKey{}, false, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	key, err := s.repo.CreateExtractionKey(ctx, repository.CreateExtractionKeyParams{
		OrganizationID: params.OrganizationID,
		KeyName:        keyName,
		SourceQuery:    sourceQuery,
		Description:    pgtype.Text{String: sourceQuery, Valid: true},
		DataType:       inferExtractionDataType(sourceQuery),
		IsRequired:     false,
	})
	if err != nil {
		if pgutil.IsUniqueViolation(err, "uq_extraction_keys_org_key") {
			existing, getErr := s.repo.GetExtractionKeyByOrgAndKeyName(ctx, repository.GetExtractionKeyByOrgAndKeyNameParams{
				OrganizationID: params.OrganizationID,
				KeyName:        keyName,
			})
			if getErr == nil {
				return existing, true, nil
			}
			return repository.ExtractionKey{}, false, errs.New(errs.CodeInternalError, "internal server error", getErr)
		}
		return repository.ExtractionKey{}, false, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	return key, false, nil
}

// multiUnderscore collapses repeated separators after key-name normalization.
var multiUnderscore = regexp.MustCompile(`_+`)

// cyrillicTransliteration maps Cyrillic runes to ASCII fragments used by the
// deterministic fallback normalizer. The long-term target is LLM-generated
// English key names validated against the same ASCII snake_case contract.
var cyrillicTransliteration = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh",
	'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o",
	'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "h", 'ц': "ts",
	'ч': "ch", 'ш': "sh", 'щ': "sch", 'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

// normalizeExtractionKeyName produces a stable ASCII snake_case technical key
// from the user's question. It is intentionally a deterministic fallback; a
// future LLM-based resolver can generate better English names while keeping this
// DB/API contract and validation shape.
func normalizeExtractionKeyName(query string) string {
	var b strings.Builder
	if len(query) > 80 {
		b.Grow(80)
	} else {
		b.Grow(len(query))
	}
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(query)) {
		if mapped, ok := transliterateRune(r); ok {
			b.WriteString(mapped)
			lastUnderscore = false
			continue
		}
		if isASCIIAlphaNum(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	key := strings.Trim(multiUnderscore.ReplaceAllString(b.String(), "_"), "_")
	if key == "" {
		return ""
	}
	if len(key) > 80 {
		key = strings.TrimRight(key[:80], "_")
	}
	return key
}

// isASCIIAlphaNum allows only ASCII letters and digits into key_name. Other
// scripts are either transliterated explicitly or treated as separators.
func isASCIIAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// inferExtractionDataType applies a small deterministic heuristic for the
// worker-facing data_type until a richer schema/LLM classifier is introduced.
func inferExtractionDataType(query string) string {
	q := strings.ToLower(query)
	switch {
	case strings.Contains(q, "%") || strings.Contains(q, "процент") || strings.Contains(q, "percent"):
		return "number"
	case strings.Contains(q, "дата") || strings.Contains(q, "срок") || strings.Contains(q, "date"):
		return "date"
	case strings.Contains(q, "есть ли") || strings.Contains(q, "наличие") || strings.Contains(q, "is "):
		return "boolean"
	default:
		return "string"
	}
}

// transliterateRune applies the explicitly defined Cyrillic transliteration
// mapping used to produce stable snake_case fallback keys.
func transliterateRune(r rune) (string, bool) {
	v, ok := cyrillicTransliteration[r]
	return v, ok
}

// extractionKeyPayloads is the compact schema sent to the Python extract worker
// in Celery kwargs. It deliberately omits timestamps and tenant fields; the task
// already scopes the worker run to one document/organization.
func extractionKeyPayloads(ctx context.Context, repo repository.Querier, orgID uuid.UUID) ([]map[string]any, error) {
	keys, err := repo.ListExtractionKeyPayloadsByOrganization(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("list extraction keys: %w", err)
	}
	payload := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		item := map[string]any{
			"id":           key.ID.String(),
			"key_name":     key.KeyName,
			"source_query": key.SourceQuery,
			"data_type":    key.DataType,
			"is_required":  key.IsRequired,
		}
		if key.Description.Valid {
			item["description"] = key.Description.String
		}
		payload = append(payload, item)
	}
	return payload, nil
}
