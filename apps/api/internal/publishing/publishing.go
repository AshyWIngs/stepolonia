// Package publishing реализует редакционные команды и безопасное публичное чтение.
package publishing

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"
)

const (
	articleNotFoundCode = "article_not_found"
	invalidLanguageCode = "invalid_language"
)

type Kind string

const (
	Validation Kind = "validation"
	NotFound   Kind = "not_found"
	Forbidden  Kind = "forbidden"
	Conflict   Kind = "conflict"
	Dependency Kind = "dependency_failure"
)

// Error содержит только безопасный код; SQL, параметры и credentials наружу не попадают.
type Error struct {
	Kind     Kind
	Code     string
	sqlState string
}

func (e *Error) Error() string { return "publishing: " + e.Code }

// SQLState сохраняет код для диагностики, не раскрывая SQL или параметры запроса.
func (e *Error) SQLState() string { return e.sqlState }

// FieldPatch отличает отсутствующее поле от явного NULL без универсального patch-механизма.
type FieldPatch struct {
	Set   bool
	Value *string
}
type TranslationPatch struct{ Title, Slug, BodyMarkdown FieldPatch }
type ArticleState struct {
	ID                  string
	Version             int64
	PublishedRevisionID *string
}
type Translation struct{ LanguageCode, Title, Slug, BodyMarkdown string }
type DraftTranslation struct {
	LanguageCode              string
	Title, Slug, BodyMarkdown *string
	IsReady                   bool
}
type EditorialArticle struct {
	ArticleState
	Drafts    []DraftTranslation
	Published []Translation
}
type Page struct{ Limit, Offset int }
type Language struct{ Code, Name, NativeName, Direction string }
type PublicArticle struct{ ArticleID, RevisionID, LanguageCode, Title, Slug, BodyMarkdown string }
type Resolution struct {
	Article  PublicArticle
	Redirect bool
}

func problem(kind Kind, code string) error { return &Error{Kind: kind, Code: code} }

func databaseError(err error) error {
	if err == nil {
		return nil
	}
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) {
		return problem(Dependency, "database_unavailable")
	}
	kind, code := Dependency, "database_unavailable"
	switch pgerr.Code {
	case "42501":
		kind, code = Forbidden, "insufficient_privileges"
	case "23505":
		kind, code = Conflict, "slug_conflict"
	case "40001", "40P01":
		kind, code = Conflict, "concurrent_change"
	case "23503":
		kind, code = Validation, "unknown_reference"
	case "23514", "23502", "22021", "22P02":
		kind, code = Validation, "invalid_translation"
	}
	return &Error{Kind: kind, Code: code, sqlState: pgerr.Code}
}

func normalizeLanguage(code string) (string, error) {
	if strings.ContainsAny(code, "_@. ") {
		return "", problem(Validation, invalidLanguageCode)
	}
	tag, err := language.Parse(code)
	if err != nil || code == "" {
		return "", problem(Validation, invalidLanguageCode)
	}
	normalized := strings.ToLower(tag.String())
	if len(normalized) < 2 || len(normalized) > 63 {
		return "", problem(Validation, invalidLanguageCode)
	}
	return normalized, nil
}

func validateID(id string) error {
	var value pgtype.UUID
	if err := value.Scan(id); err != nil || !value.Valid {
		return problem(Validation, "invalid_article_id")
	}
	return nil
}

func validatePage(page Page) error {
	if page.Limit < 1 || page.Limit > 100 || page.Offset < 0 {
		return problem(Validation, "invalid_page")
	}
	return nil
}

func normalizePatch(input map[string]TranslationPatch) (map[string]TranslationPatch, error) {
	result := make(map[string]TranslationPatch, len(input))
	for code, patch := range input {
		normalized, err := normalizeLanguage(code)
		if err != nil {
			return nil, err
		}
		if _, exists := result[normalized]; exists {
			return nil, problem(Validation, "duplicate_language")
		}
		for _, field := range []FieldPatch{patch.Title, patch.Slug, patch.BodyMarkdown} {
			if !field.Set && field.Value != nil {
				return nil, problem(Validation, "invalid_patch")
			}
		}
		result[normalized] = patch
	}
	return result, nil
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", problem(Dependency, "identifier_unavailable")
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
