package siteconfig

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"regexp"

	"stepolonia/apps/api/internal/staticsvg"
)

const (
	PendingDraftCode  = "site_draft_pending"
	InvalidAccentCode = "invalid_site_accent"
)

var accentValue = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type ScopedPublication struct {
	AppliedRevision
	PreviousVersion int64
	ScopeVersion    string
	Value           string
}

type PortalDetails struct {
	Title       string `json:"title" minLength:"1" maxLength:"200"`
	Tagline     string `json:"tagline" maxLength:"300"`
	Description string `json:"description" maxLength:"2000"`
	Notice      string `json:"notice" maxLength:"2000"`
}

func (s Settings) Details() PortalDetails {
	return PortalDetails{s.Title, s.Tagline, s.Description, s.Notice}
}

func (d PortalDetails) Version() string {
	return settingsScopeVersion(d)
}

func settingsScopeVersion(payload any) string {
	raw, _ := json.Marshal(payload)
	return ScopeVersion(string(raw))
}

// ScopeVersion identifies a saved value within its command's resource.
func ScopeVersion(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func ApplyAppearanceTx(ctx context.Context, tx *sql.Tx, expected, accent string) (ScopedPublication, error) {
	if !accentValue.MatchString(accent) {
		return ScopedPublication{}, failure(400, InvalidAccentCode)
	}
	state, err := lockPublishedSettings(ctx, tx)
	if err != nil {
		return ScopedPublication{}, err
	}
	if ScopeVersion(state.settings.Accent) != expected {
		return ScopedPublication{}, failure(409, versionConflictCode)
	}
	state.settings.Accent = accent
	return publishSettingsChange(ctx, tx, state, "", accent)
}

func ApplyLanguageFlagTx(ctx context.Context, tx *sql.Tx, expected, language, source string) (ScopedPublication, error) {
	code, err := normalize(language)
	if err != nil {
		return ScopedPublication{}, failure(400, InvalidLanguageFlagCode)
	}
	flag, err := staticsvg.Normalize(source)
	if err != nil {
		return ScopedPublication{}, failure(400, InvalidLanguageFlagCode)
	}
	state, err := lockPublishedSettings(ctx, tx)
	if err != nil {
		return ScopedPublication{}, err
	}
	var current string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(f.svg,l.initial_flag_svg)
 FROM public.languages l LEFT JOIN public.site_revision_language_flags f
 ON f.revision_id=$1 AND f.language_code=l.code WHERE l.code=$2 AND l.is_enabled`, state.id, code).Scan(&current)
	if err == sql.ErrNoRows {
		return ScopedPublication{}, failure(400, InvalidLanguageFlagCode)
	}
	if err != nil {
		return ScopedPublication{}, dbError(err)
	}
	if ScopeVersion(current) != expected {
		return ScopedPublication{}, failure(409, versionConflictCode)
	}
	return publishSettingsChange(ctx, tx, state, code, flag)
}

type publishedSettings struct {
	id       string
	settings Settings
	version  int64
}

func lockPublishedSettings(ctx context.Context, tx *sql.Tx) (publishedSettings, error) {
	if err := checkEditorRole(ctx, tx); err != nil {
		return publishedSettings{}, err
	}
	var state publishedSettings
	var draft bool
	fields := &state.settings
	err := tx.QueryRowContext(ctx, `SELECT r.id::text,r.accent,r.title,r.tagline,r.description,r.notice,
 r.default_language,r.fallback_language,s.version,s.draft_id IS NOT NULL
 FROM public.site_state s JOIN public.site_revisions r ON r.id=s.published_id
 WHERE s.singleton FOR UPDATE OF s`).Scan(&state.id, &fields.Accent, &fields.Title, &fields.Tagline,
		&fields.Description, &fields.Notice, &fields.DefaultLanguage, &fields.FallbackLanguage, &state.version, &draft)
	if err != nil {
		return publishedSettings{}, dbError(err)
	}
	if draft {
		return publishedSettings{}, failure(409, PendingDraftCode)
	}
	return state, nil
}

func publishSettingsChange(ctx context.Context, tx *sql.Tx, state publishedSettings, language, value string) (ScopedPublication, error) {
	id, err := createScopedRevision(ctx, tx, state, true)
	if err != nil {
		return ScopedPublication{}, err
	}
	if language != "" {
		if err := storeLanguageFlag(ctx, tx, id, language, value); err != nil {
			return ScopedPublication{}, err
		}
	}
	result, err := activateScopedRevision(ctx, tx, state, id, ScopeVersion(value))
	result.Value = value
	return result, err
}

func createScopedRevision(ctx context.Context, tx *sql.Tx, state publishedSettings, copyRules bool) (string, error) {
	return createScopedRevisionExceptCatalog(ctx, tx, state, copyRules, "")
}

func createScopedRevisionExceptCatalog(ctx context.Context, tx *sql.Tx, state publishedSettings, copyRules bool, exceptCatalog string) (string, error) {
	id, err := identifier()
	if err != nil {
		return "", err
	}
	if err := copyPublishedSettings(ctx, tx, id, state, exceptCatalog); err != nil {
		return "", err
	}
	if copyRules {
		query := `INSERT INTO public.site_revision_slug_rules SELECT $1,language_code,source_character,replacement
 FROM public.site_revision_slug_rules WHERE revision_id=$2`
		if _, err := tx.ExecContext(ctx, query, id, state.id); err != nil {
			return "", dbError(err)
		}
	}
	return id, nil
}

func activateScopedRevision(ctx context.Context, tx *sql.Tx, state publishedSettings, id, version string) (ScopedPublication, error) {
	if err := activateRevision(ctx, tx, id); err != nil {
		return ScopedPublication{}, err
	}
	return ScopedPublication{
		AppliedRevision: AppliedRevision{PublicationID: id, Version: state.version + 1},
		PreviousVersion: state.version, ScopeVersion: version,
	}, nil
}

func copyPublishedSettings(ctx context.Context, tx *sql.Tx, id string, state publishedSettings, exceptCatalog string) error {
	fields := state.settings
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.site_revisions(id,title,description,default_language,fallback_language,tagline,notice,accent)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, fields.Title, fields.Description, fields.DefaultLanguage, fields.FallbackLanguage, fields.Tagline, fields.Notice, fields.Accent); err != nil {
		return dbError(err)
	}
	for _, query := range []string{
		`INSERT INTO public.site_revision_keys SELECT $1,message_key,placeholders FROM public.site_revision_keys WHERE revision_id=$2`,
		`INSERT INTO public.site_revision_language_flags SELECT $1,language_code,svg FROM public.site_revision_language_flags WHERE revision_id=$2`,
	} {
		if _, err := tx.ExecContext(ctx, query, id, state.id); err != nil {
			return dbError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.site_revision_languages(revision_id,language_code,catalog_revision_id,name,native_name,direction)
 SELECT $1,language_code,COALESCE(catalog_revision_id,revision_id),name,native_name,direction
 FROM public.site_revision_languages WHERE revision_id=$2 AND language_code<>$3`, id, state.id, exceptCatalog); err != nil {
		return dbError(err)
	}
	return nil
}
