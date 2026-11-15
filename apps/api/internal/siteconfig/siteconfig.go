// Package siteconfig хранит настройки и UI как единую атомарную публикацию установки.
package siteconfig

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"
)

const (
	unavailableCode     = "site_configuration_unavailable"
	versionConflictCode = "site_version_conflict"
)

type Settings struct {
	Tagline          string `json:"tagline" maxLength:"300"`
	Notice           string `json:"notice" maxLength:"2000"`
	Accent           string `json:"accent" pattern:"^#[0-9a-fA-F]{6}$"`
	Title            string `json:"title" maxLength:"200"`
	Description      string `json:"description" maxLength:"2000"`
	DefaultLanguage  string `json:"default_language"`
	FallbackLanguage string `json:"fallback_language"`
}
type Language struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	NativeName string `json:"native_name"`
	Direction  string `json:"direction" enum:"ltr,rtl"`
}
type Publication struct {
	Version           string            `json:"version" format:"uuid"`
	Settings          Settings          `json:"settings"`
	RequestedLanguage string            `json:"requested_language" doc:"Normalized requested UI language; empty when omitted."`
	Language          Language          `json:"language"`
	Languages         []Language        `json:"languages" nullable:"false"`
	Messages          map[string]string `json:"messages"`
}
type Draft struct {
	Settings Settings
	Catalogs map[string]map[string]string
}
type Error struct {
	Status int
	Code   string
	cause  error
}

func (e *Error) Error() string              { return "siteconfig: " + e.Code }
func (e *Error) Unwrap() error              { return e.cause }
func failure(status int, code string) error { return &Error{Status: status, Code: code} }
func dbError(err error) error {
	if err == nil {
		return nil
	}
	status, code := 503, unavailableCode
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "23502", "23503", "23514", "22P02":
			status, code = 400, "invalid_site_configuration"
		case "40001", "40P01", "23505":
			status, code = 409, versionConflictCode
		case "42501":
			status, code = 403, "editorial_access_denied"
		}
	}
	return &Error{Status: status, Code: code, cause: err}
}
func normalize(code string) (string, error) {
	if code == "" || strings.ContainsAny(code, "_@. \t\r\n") {
		return "", failure(400, "invalid_language")
	}
	tag, err := language.Parse(code)
	value := strings.ToLower(tag.String())
	if err != nil || len(value) < 2 || len(value) > 63 {
		return "", failure(400, "invalid_language")
	}
	return value, nil
}
func validUUID(value string) bool {
	var id pgtype.UUID
	return len(value) == 36 && value[8] == '-' && value[13] == '-' && value[18] == '-' && value[23] == '-' && id.Scan(value) == nil && id.Valid
}
func identifier() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", dbError(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }
func (s *Service) begin(ctx context.Context, expected int64) (*sql.Tx, sql.NullString, error) {
	if expected < 1 {
		return nil, sql.NullString{}, failure(400, "invalid_version")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, sql.NullString{}, dbError(err)
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, "SELECT pg_catalog.pg_has_role(current_user,'sp_publisher','USAGE')").Scan(&allowed)
	if err != nil || !allowed {
		_ = tx.Rollback()
		if err != nil {
			return nil, sql.NullString{}, dbError(err)
		}
		return nil, sql.NullString{}, failure(403, "editorial_access_denied")
	}
	var version int64
	var draft sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT version,draft_id::text FROM public.site_state WHERE singleton FOR UPDATE").Scan(&version, &draft)
	if err != nil || version != expected {
		_ = tx.Rollback()
		if err != nil {
			return nil, draft, dbError(err)
		}
		return nil, draft, failure(409, versionConflictCode)
	}
	return tx, draft, nil
}

// SaveDraft заменяет весь черновик; общий singleton lock исключает потерянные изменения.
func (s *Service) SaveDraft(ctx context.Context, expected int64, input Draft) (int64, error) {
	input, err := normalizeDraft(input)
	if err != nil {
		return 0, err
	}
	tx, old, err := s.begin(ctx, expected)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := identifier()
	if err != nil {
		return 0, err
	}
	if err = storeDraft(ctx, tx, id, input); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE public.site_state SET draft_id=$1,version=version+1 WHERE singleton`, id)
	if err != nil {
		return 0, dbError(err)
	}
	if old.Valid {
		if err = deleteDraft(ctx, tx, old.String); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, dbError(err)
	}
	return expected + 1, nil
}

func (s *Service) Publish(ctx context.Context, expected int64) (string, int64, error) {
	tx, draft, err := s.begin(ctx, expected)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if !draft.Valid {
		return "", 0, failure(409, "site_draft_missing")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE public.site_revisions SET sealed=true WHERE id=$1`, draft.String); err != nil {
		return "", 0, dbError(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE public.site_state SET published_id=$1,draft_id=NULL,version=version+1 WHERE singleton`, draft.String); err != nil {
		return "", 0, dbError(err)
	}
	if err = tx.Commit(); err != nil {
		return "", 0, dbError(err)
	}
	return draft.String, expected + 1, nil
}

type Reader struct{ db *sql.DB }

func NewReader(db *sql.DB) *Reader { return &Reader{db: db} }
func (r *Reader) Read(ctx context.Context, requested, version string) (Publication, error) {
	result := Publication{Languages: []Language{}, Messages: map[string]string{}}
	if requested != "" {
		value, err := normalize(requested)
		if err != nil {
			return result, err
		}
		result.RequestedLanguage = value
	}
	if version != "" && !validUUID(version) {
		return result, failure(400, "invalid_version")
	}
	if r == nil || r.db == nil {
		return result, failure(503, unavailableCode)
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return result, dbError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = checkPublicRole(ctx, tx); err != nil {
		return result, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id::text,title,description,default_language,fallback_language,tagline,notice,accent FROM public.public_site`).Scan(&result.Version, &result.Settings.Title, &result.Settings.Description, &result.Settings.DefaultLanguage, &result.Settings.FallbackLanguage, &result.Settings.Tagline, &result.Settings.Notice, &result.Settings.Accent)
	if err != nil {
		return result, dbError(err)
	}
	if version != "" && !strings.EqualFold(version, result.Version) {
		return result, failure(409, versionConflictCode)
	}
	if err = readCatalog(ctx, tx, &result); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, dbError(err)
	}
	return result, nil
}

func normalizeDraft(input Draft) (Draft, error) {
	var err error
	input.Settings.DefaultLanguage, err = normalize(input.Settings.DefaultLanguage)
	if err != nil {
		return Draft{}, err
	}
	input.Settings.FallbackLanguage, err = normalize(input.Settings.FallbackLanguage)
	if err != nil {
		return Draft{}, err
	}
	catalogs := make(map[string]map[string]string, len(input.Catalogs))
	for code, messages := range input.Catalogs {
		normalized, e := normalize(code)
		if e != nil {
			return Draft{}, e
		}
		if _, exists := catalogs[normalized]; exists {
			return Draft{}, failure(400, "duplicate_language")
		}
		catalogs[normalized] = messages
	}

	input.Catalogs = catalogs
	return input, nil
}

func storeDraft(ctx context.Context, tx *sql.Tx, id string, input Draft) error {
	v := input.Settings
	_, err := tx.ExecContext(ctx, `INSERT INTO public.site_revisions(id,title,description,default_language,fallback_language,tagline,notice,accent) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, v.Title, v.Description, v.DefaultLanguage, v.FallbackLanguage, v.Tagline, v.Notice, v.Accent)
	if err != nil {
		return dbError(err)
	}
	// Дефиниции копируются в снимок, чтобы последующая смена приложения не меняла публикацию.
	_, err = tx.ExecContext(ctx, `INSERT INTO public.site_revision_keys SELECT $1,message_key,placeholders FROM public.ui_message_keys`, id)
	if err != nil {
		return dbError(err)
	}
	for code, messages := range input.Catalogs {
		if err := storeCatalog(ctx, tx, id, code, messages); err != nil {
			return err
		}
	}
	return nil
}

func storeCatalog(ctx context.Context, tx *sql.Tx, id, code string, messages map[string]string) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO public.site_revision_languages SELECT $1,code,name,native_name,direction FROM public.languages WHERE code=$2`, id, code)
	if err != nil {
		return dbError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return dbError(err)
	}
	if count != 1 {
		return failure(400, "language_unavailable")
	}
	for key, value := range messages {
		if _, err = tx.ExecContext(ctx, `INSERT INTO public.site_revision_messages VALUES($1,$2,$3,$4)`, id, code, key, value); err != nil {
			return dbError(err)
		}
	}
	return nil
}
func deleteDraft(ctx context.Context, tx *sql.Tx, id string) error {
	for _, query := range []string{
		"DELETE FROM public.site_revision_messages WHERE revision_id=$1",
		"DELETE FROM public.site_revision_languages WHERE revision_id=$1",
		"DELETE FROM public.site_revision_keys WHERE revision_id=$1",
		"DELETE FROM public.site_revisions WHERE id=$1",
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return dbError(err)
		}
	}
	return nil
}

func checkPublicRole(ctx context.Context, tx *sql.Tx) error {
	// Только отдельное непривилегированное runtime-подключение может обслуживать публичный контракт.
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT current_user=session_user AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
 AND NOT pg_catalog.has_schema_privilege(oid,'public','CREATE')
 AND NOT pg_catalog.has_database_privilege(oid,current_database(),'CREATE,TEMP')
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
 AND NOT EXISTS(SELECT 1 FROM (VALUES('public.languages'),('public.articles'),('public.article_draft_translations'),('public.article_revisions'),('public.article_revision_translations'),('public.public_routes'),('public.site_state'),('public.site_revisions'),('public.ui_message_keys'),('public.site_revision_keys'),('public.site_revision_languages'),('public.site_revision_messages')) AS p(name)
 WHERE pg_catalog.has_table_privilege(r.oid,p.name,'SELECT') OR pg_catalog.has_any_column_privilege(r.oid,p.name,'SELECT'))
 FROM pg_catalog.pg_roles r WHERE rolname=current_user`).Scan(&allowed)
	if err != nil {
		return dbError(err)
	}
	if !allowed {
		return failure(503, "public_connection_required")
	}
	return nil
}

func readCatalog(ctx context.Context, tx *sql.Tx, result *Publication) error {
	languages, err := readLanguages(ctx, tx, result.Version)
	if err != nil {
		return err
	}
	result.Languages = languages
	result.Language = selectLanguage(languages, result.RequestedLanguage, result.Settings.FallbackLanguage)
	if result.Language.Code == "" {
		return failure(503, unavailableCode)
	}
	messages, err := readMessages(ctx, tx, result.Version, result.Language.Code)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return failure(503, unavailableCode)
	}
	result.Messages = messages
	return nil
}
func selectLanguage(languages []Language, requested, fallback string) Language {
	var result Language
	for _, value := range languages {
		if value.Code == requested {
			return value
		}
		if value.Code == fallback {
			result = value
		}
	}
	return result
}
func readLanguages(ctx context.Context, tx *sql.Tx, version string) ([]Language, error) {
	rows, err := tx.QueryContext(ctx, `SELECT language_code,name,native_name,direction FROM public.public_ui_languages WHERE revision_id=$1 ORDER BY language_code COLLATE "C"`, version)
	if err != nil {
		return nil, dbError(err)
	}
	defer func() { _ = rows.Close() }()
	result := []Language{}
	for rows.Next() {
		var value Language
		if err = rows.Scan(&value.Code, &value.Name, &value.NativeName, &value.Direction); err != nil {
			return nil, dbError(err)
		}
		result = append(result, value)
	}
	return result, dbError(rows.Err())
}
func readMessages(ctx context.Context, tx *sql.Tx, version, code string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT message_key,message_value FROM public.public_ui_messages WHERE revision_id=$1 AND language_code=$2`, version, code)
	if err != nil {
		return nil, dbError(err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string]string{}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return nil, dbError(err)
		}
		result[key] = value
	}
	return result, dbError(rows.Err())
}
