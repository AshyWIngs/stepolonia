// Package siteconfig manages atomic publications of site settings and UI catalogs.
package siteconfig

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"
)

type Settings struct {
	Tagline          string `json:"tagline" maxLength:"300" doc:"Short site tagline. An empty string means no tagline." example:"Historie naszej społeczności"`
	Notice           string `json:"notice" maxLength:"2000" doc:"Optional site notice as plain text. An empty string means no notice." example:""`
	Accent           string `json:"accent" pattern:"^#[0-9a-fA-F]{6}$" doc:"Six-digit hexadecimal accent color, including the leading #." example:"#2563eb"`
	Title            string `json:"title" maxLength:"200" doc:"Human-readable title of this installation." example:"Portal"`
	Description      string `json:"description" maxLength:"2000" doc:"Published site description as plain text." example:"Poznaj historie i wydarzenia naszej społeczności."`
	DefaultLanguage  string `json:"default_language" doc:"Configured default content language. This does not select the UI catalog." example:"pl"`
	FallbackLanguage string `json:"fallback_language" doc:"UI catalog used when lang is omitted or its complete published catalog is unavailable." example:"pl"`
}
type Language struct {
	Code       string `json:"code" doc:"Normalized BCP 47 language code." example:"pl"`
	Name       string `json:"name" doc:"Human-readable display name of the language." example:"Polish"`
	NativeName string `json:"native_name" doc:"Language name in its own language." example:"Polski"`
	Direction  string `json:"direction" enum:"ltr,rtl" doc:"Text direction for this language." example:"ltr"`
}
type Publication struct {
	Version           string            `json:"version" format:"uuid" doc:"Opaque UUID of this atomic settings and catalog publication. Supply it as version to detect publication changes." example:"33333333-3333-4333-8333-333333333333"`
	Settings          Settings          `json:"settings" doc:"Site settings from the same atomic publication as messages."`
	RequestedLanguage string            `json:"requested_language" doc:"Normalized requested UI language; empty when omitted."`
	Language          Language          `json:"language" doc:"Language of the complete returned UI catalog, after fallback."`
	Languages         []Language        `json:"languages" nullable:"false" doc:"Published active UI languages ordered by code. Each has a complete catalog in this publication."`
	Messages          map[string]string `json:"messages" doc:"Complete catalog keyed by registered UI message key. Values are message templates and may contain named placeholders. The entire catalog uses language.code."`
	SlugRules         []SlugRule        `json:"slug_rules" nullable:"false" doc:"Published slug suggestion rules ordered by content language code and source character."`
	LanguageFlags     map[string]string `json:"language_flags,omitempty" doc:"Static SVG flags for enabled languages, keyed by exact language code. Missing entries use the standard globe."`
}
type Draft struct {
	Settings      Settings
	Catalogs      map[string]map[string]string
	SlugRules     []SlugRule
	LanguageFlags map[string]string
}

const (
	unavailableCode     = "site_configuration_unavailable"
	invalidVersionCode  = "invalid_version"
	versionConflictCode = "site_version_conflict"
)

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
	return (pgtype.UUID{Bytes: b, Valid: true}).String(), nil
}

type Reader struct{ db *sql.DB }

func NewReader(db *sql.DB) *Reader { return &Reader{db: db} }
func (r *Reader) Read(ctx context.Context, requested, version string) (Publication, error) {
	result := Publication{Languages: []Language{}, Messages: map[string]string{}, SlugRules: []SlugRule{}}
	if requested != "" {
		value, err := normalize(requested)
		if err != nil {
			return result, err
		}
		result.RequestedLanguage = value
	}
	if version != "" && !validUUID(version) {
		return result, failure(400, invalidVersionCode)
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
	if err = readLocalization(ctx, tx, &result); err != nil {
		return result, err
	}
	result.SlugRules, err = readSlugRules(ctx, tx, publishedSlugRulesQuery, result.Version)
	if err != nil {
		return result, dbError(err)
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
	input.LanguageFlags, err = normalizeLanguageFlags(input.LanguageFlags)
	if err != nil {
		return Draft{}, err
	}
	input.SlugRules, err = normalizeSlugRules(input.SlugRules)
	if err != nil {
		return Draft{}, err
	}
	return input, nil
}

func storeDraft(ctx context.Context, tx *sql.Tx, id string, input Draft) error {
	v := input.Settings
	_, err := tx.ExecContext(ctx, `INSERT INTO public.site_revisions(id,title,description,default_language,fallback_language,tagline,notice,accent) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, v.Title, v.Description, v.DefaultLanguage, v.FallbackLanguage, v.Tagline, v.Notice, v.Accent)
	if err != nil {
		return dbError(err)
	}
	// Copy definitions into the snapshot so later application changes leave it stable.
	_, err = tx.ExecContext(ctx, `INSERT INTO public.site_revision_keys SELECT $1,message_key,placeholders FROM public.ui_message_keys`, id)
	if err != nil {
		return dbError(err)
	}
	for code, messages := range input.Catalogs {
		if err := storeCatalog(ctx, tx, id, code, messages); err != nil {
			return err
		}
	}
	if err = storeLanguageFlags(ctx, tx, id, input.LanguageFlags); err != nil {
		return err
	}
	return storeSlugRules(ctx, tx, id, input.SlugRules)
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
		"DELETE FROM public.site_revision_language_flags WHERE revision_id=$1",
		"DELETE FROM public.site_revision_slug_rules WHERE revision_id=$1",
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
	// Serve the public contract through a separate unprivileged runtime connection.
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT current_user=session_user AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
 AND NOT pg_catalog.has_schema_privilege(oid,'public','CREATE')
 AND NOT pg_catalog.has_database_privilege(oid,current_database(),'CREATE,TEMP')
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c WHERE c.relnamespace='public'::regnamespace AND c.relkind='r'
 AND (pg_catalog.has_table_privilege(r.oid,c.oid,'SELECT') OR pg_catalog.has_any_column_privilege(r.oid,c.oid,'SELECT')))
 FROM pg_catalog.pg_roles r WHERE rolname=current_user`).Scan(&allowed)
	if err != nil {
		return dbError(err)
	}
	if !allowed {
		return failure(503, "public_connection_required")
	}
	return nil
}

func readLocalization(ctx context.Context, tx *sql.Tx, result *Publication) error {
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
	result.LanguageFlags, err = readLanguageFlags(ctx, tx, publishedLanguageFlagsQuery, result.Version)
	return err
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

// TransformSchema documents the public Settings contract without changing validation.
func (Settings) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "Published installation settings. Every property is present. Optional display text uses empty strings."
	return schema
}

// TransformSchema documents the public Language contract without changing validation.
func (Language) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "Published UI language metadata used for catalog selection and text direction."
	return schema
}

// TransformSchema documents the public Publication contract without changing validation.
func (Publication) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "One atomic site publication containing settings, slug suggestion rules, available UI languages and one complete catalog. requested_language records the normalized request or an empty string. language identifies the actual catalog after fallback. No draft or historical revision is exposed."
	return schema
}
