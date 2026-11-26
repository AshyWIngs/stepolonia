package siteconfig

import "context"

type uiAudience uint8

const (
	visitorUI uiAudience = iota
	entryUI
	adminUI
)

// These keys are used on the entry screen, including restricted enrollment and recovery.
var adminEntryKeys = []string{
	"admin.account_codes", "admin.account_codes_help", "admin.account_done",
	"admin.account_factor_help", "admin.account_new_password", "admin.account_validation",
	"admin.cancel", "admin.changed", "admin.checking", "admin.code", "admin.code_help", "admin.continue",
	"admin.entry_activation", "admin.entry_enrollment", "admin.entry_interrupted",
	"admin.entry_invalid", "admin.entry_invitation", "admin.entry_recovery", "admin.entry_recovery_code",
	"admin.invalid_code", "admin.invalid_credentials",
	"admin.limited", "admin.login", "admin.name", "admin.password", "admin.restart", "admin.restricted",
	"admin.retry", "admin.sign_in", "admin.sign_in_retry", "admin.signed_out",
	"admin.two_step", "admin.unavailable", "admin.verify", "admin.working",
	"common.language", "theme.dark", "theme.label", "theme.light", "theme.system",
}

type publicationSnapshot struct {
	Publication
	slugRules []SlugRule
}

// AdminEntry contains the published branding and text needed before a full session.
type AdminEntry struct {
	Version       string            `json:"version" format:"uuid" doc:"Identifier of the atomic publication used for this response."`
	Title         string            `json:"title" doc:"Published title of the installation."`
	Language      Language          `json:"language" doc:"Actual UI language after fallback."`
	Languages     []Language        `json:"languages" nullable:"false" doc:"Published UI languages available at sign-in."`
	Messages      map[string]string `json:"messages" doc:"Localized entry, login, MFA, enrollment and recovery messages."`
	LanguageFlags map[string]string `json:"language_flags,omitempty" doc:"Published static SVG flags. Missing flags use the globe icon."`
}

// AdminCatalog contains the currently published messages and defaults used by the workspace.
type AdminCatalog struct {
	AdminEntry
	DefaultLanguage string     `json:"default_language" doc:"Published default language for content."`
	SlugRules       []SlugRule `json:"slug_rules" nullable:"false" doc:"Published slug suggestions ordered by content language and source character."`
}

func (r *Reader) ReadAdminEntry(ctx context.Context, requested string) (AdminEntry, error) {
	result, err := r.read(ctx, requested, "", entryUI, false)
	return adminEntry(result.Publication), err
}

func (r *Reader) ReadAdminCatalog(ctx context.Context, requested string, mayReadSlugRules bool) (AdminCatalog, error) {
	result, err := r.read(ctx, requested, "", adminUI, mayReadSlugRules)
	return AdminCatalog{
		AdminEntry: adminEntry(result.Publication), DefaultLanguage: result.Settings.DefaultLanguage, SlugRules: result.slugRules,
	}, err
}

func adminEntry(publication Publication) AdminEntry {
	return AdminEntry{
		Version: publication.Version, Title: publication.Settings.Title, Language: publication.Language,
		Languages: publication.Languages, Messages: publication.Messages, LanguageFlags: publication.LanguageFlags,
	}
}
