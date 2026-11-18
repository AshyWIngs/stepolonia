package siteconfig

import (
	"context"
	"database/sql"
)

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func checkEditorRole(ctx context.Context, tx *sql.Tx) error {
	var allowed bool
	err := tx.QueryRowContext(ctx, "SELECT pg_catalog.pg_has_role(current_user,'sp_publisher','USAGE')").Scan(&allowed)
	if err != nil {
		return dbError(err)
	}
	if !allowed {
		return failure(403, "editorial_access_denied")
	}
	return nil
}

func lockDraft(ctx context.Context, tx *sql.Tx, expected int64) (sql.NullString, error) {
	if expected < 1 {
		return sql.NullString{}, failure(400, invalidVersionCode)
	}
	if err := checkEditorRole(ctx, tx); err != nil {
		return sql.NullString{}, err
	}
	var version int64
	var draft sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT version,draft_id::text FROM public.site_state WHERE singleton FOR UPDATE").Scan(&version, &draft)
	if err != nil {
		return sql.NullString{}, dbError(err)
	}
	if version != expected {
		return sql.NullString{}, failure(409, versionConflictCode)
	}
	return draft, nil
}

// SaveDraft replaces the draft under the singleton lock to prevent lost updates.
func (s *Service) SaveDraft(ctx context.Context, expected int64, input Draft) (int64, error) {
	input, err := normalizeDraft(input)
	if err != nil {
		return 0, err
	}
	if expected < 1 {
		return 0, failure(400, invalidVersionCode)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, dbError(err)
	}
	defer func() { _ = tx.Rollback() }()
	version, err := saveNormalizedDraft(ctx, tx, expected, input)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, dbError(err)
	}
	return version, nil
}

// SaveDraftTx replaces the draft without committing. The caller owns
// authorization for the acting account and commits its audit in the same transaction.
func SaveDraftTx(ctx context.Context, tx *sql.Tx, expected int64, input Draft) (int64, error) {
	input, err := normalizeDraft(input)
	if err != nil {
		return 0, err
	}
	return saveNormalizedDraft(ctx, tx, expected, input)
}

func saveNormalizedDraft(ctx context.Context, tx *sql.Tx, expected int64, input Draft) (int64, error) {
	old, err := lockDraft(ctx, tx, expected)
	if err != nil {
		return 0, err
	}
	id, err := identifier()
	if err != nil {
		return 0, err
	}
	if err = storeDraft(ctx, tx, id, input); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE public.site_state SET draft_id=$1,version=version+1 WHERE singleton`, id); err != nil {
		return 0, dbError(err)
	}
	if old.Valid {
		if err = deleteDraft(ctx, tx, old.String); err != nil {
			return 0, err
		}
	}
	return expected + 1, nil
}

func (s *Service) Publish(ctx context.Context, expected int64) (string, int64, error) {
	if expected < 1 {
		return "", 0, failure(400, invalidVersionCode)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, dbError(err)
	}
	defer func() { _ = tx.Rollback() }()
	id, version, err := PublishTx(ctx, tx, expected)
	if err != nil {
		return "", 0, err
	}
	if err = tx.Commit(); err != nil {
		return "", 0, dbError(err)
	}
	return id, version, nil
}

// PublishTx seals the draft and updates the publication pointer without committing.
// The caller owns account authorization and the transaction.
func PublishTx(ctx context.Context, tx *sql.Tx, expected int64) (string, int64, error) {
	draft, err := lockDraft(ctx, tx, expected)
	if err != nil {
		return "", 0, err
	}
	if !draft.Valid {
		return "", 0, failure(409, "site_draft_missing")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE public.site_revisions SET sealed=true WHERE id=$1`, draft.String); err != nil {
		return "", 0, dbError(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE public.site_state SET published_id=$1,draft_id=NULL,version=version+1 WHERE singleton`, draft.String); err != nil {
		return "", 0, dbError(err)
	}
	return draft.String, expected + 1, nil
}

type Editable struct {
	Version     int64                        `json:"version"`
	HasDraft    bool                         `json:"has_draft"`
	PublishedID string                       `json:"published_id"`
	Settings    Settings                     `json:"settings"`
	Catalogs    map[string]map[string]string `json:"catalogs"`
	Languages   []Language                   `json:"languages"`
	MessageKeys []string                     `json:"message_keys"`
}

// ReadEditable returns the current draft, or the current publication if there
// is no draft. It holds the singleton lock until the caller's transaction ends.
func ReadEditable(ctx context.Context, tx *sql.Tx) (Editable, error) {
	if err := checkEditorRole(ctx, tx); err != nil {
		return Editable{}, err
	}
	result := Editable{Catalogs: make(map[string]map[string]string)}
	var draft, published sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT version,draft_id::text,published_id::text
 FROM public.site_state WHERE singleton FOR SHARE`).Scan(&result.Version, &draft, &published)
	if err != nil {
		return Editable{}, dbError(err)
	}
	result.HasDraft = draft.Valid
	result.PublishedID = published.String
	result.Languages, err = readEditorLanguages(ctx, tx)
	if err != nil {
		return Editable{}, err
	}
	result.MessageKeys, err = readEditorMessageKeys(ctx, tx)
	if err != nil {
		return Editable{}, err
	}
	revision := published
	if draft.Valid {
		revision = draft
	}
	if !revision.Valid {
		return result, nil
	}
	if err := readEditorRevision(ctx, tx, revision.String, &result); err != nil {
		return Editable{}, err
	}
	return result, nil
}

func readEditorLanguages(ctx context.Context, tx *sql.Tx) ([]Language, error) {
	rows, err := tx.QueryContext(ctx, `SELECT code,name,native_name,direction FROM public.languages
 WHERE is_enabled ORDER BY code COLLATE "C"`)
	if err != nil {
		return nil, dbError(err)
	}
	defer func() { _ = rows.Close() }()
	languages := []Language{}
	for rows.Next() {
		var entry Language
		if err := rows.Scan(&entry.Code, &entry.Name, &entry.NativeName, &entry.Direction); err != nil {
			return nil, dbError(err)
		}
		languages = append(languages, entry)
	}
	return languages, dbError(rows.Err())
}

func readEditorMessageKeys(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT message_key FROM public.ui_message_keys ORDER BY message_key COLLATE "C"`)
	if err != nil {
		return nil, dbError(err)
	}
	defer func() { _ = rows.Close() }()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, dbError(err)
		}
		keys = append(keys, key)
	}
	return keys, dbError(rows.Err())
}

func readEditorRevision(ctx context.Context, tx *sql.Tx, revision string, result *Editable) error {
	v := &result.Settings
	err := tx.QueryRowContext(ctx, `SELECT title,description,default_language,fallback_language,tagline,notice,accent
 FROM public.site_revisions WHERE id=$1`, revision).Scan(&v.Title, &v.Description, &v.DefaultLanguage, &v.FallbackLanguage, &v.Tagline, &v.Notice, &v.Accent)
	if err != nil {
		return dbError(err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT l.language_code,m.message_key,m.message_value
 FROM public.site_revision_languages l
 LEFT JOIN public.site_revision_messages m USING (revision_id,language_code)
 WHERE l.revision_id=$1`, revision)
	if err != nil {
		return dbError(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var code string
		var key, value sql.NullString
		if err := rows.Scan(&code, &key, &value); err != nil {
			return dbError(err)
		}
		messages := result.Catalogs[code]
		if messages == nil {
			messages = make(map[string]string)
			result.Catalogs[code] = messages
		}
		if key.Valid {
			messages[key.String] = value.String
		}
	}
	return dbError(rows.Err())
}
