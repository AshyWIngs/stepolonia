package siteconfig

import (
	"context"
	"database/sql"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

const (
	MaxSlugRules            = 512
	maxSlugReplacementBytes = 16
	invalidSlugRulesCode    = "invalid_slug_rules"
)

type SlugRule struct {
	LanguageCode string `json:"language_code" minLength:"2" maxLength:"63" doc:"Registered content language code. Rules apply to this language only." example:"pl"`
	Character    string `json:"character" minLength:"1" maxLength:"1" doc:"One non-ASCII Unicode character in lowercase NFC form." example:"ł"`
	Replacement  string `json:"replacement" maxLength:"16" pattern:"^[a-z0-9-]*$" doc:"Lowercase ASCII letters, digits or hyphens. An empty string removes the character from a slug suggestion." example:"l"`
}

// TransformSchema describes how clients apply a published rule to suggestions.
func (SlugRule) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "A character replacement for slug suggestions in one content language. The source is canonicalized before matching and an empty replacement removes that character."
	return schema
}

const (
	initialSlugRulesQuery = `SELECT language_code,source_character,replacement
 FROM public.initial_slug_rules ORDER BY language_code,source_character`
	editableSlugRulesQuery = `SELECT language_code,source_character,replacement
 FROM public.site_revision_slug_rules WHERE revision_id=$1 ORDER BY language_code,source_character`
	publishedSlugRulesQuery = `SELECT language_code,source_character,replacement
 FROM public.public_slug_rules WHERE revision_id=$1 ORDER BY language_code,source_character`
)

func normalizeSlugRules(rules []SlugRule) ([]SlugRule, error) {
	if rules == nil {
		return nil, nil
	}
	if len(rules) > MaxSlugRules {
		return nil, failure(400, invalidSlugRulesCode)
	}
	result := make([]SlugRule, 0, len(rules))
	type ruleKey struct{ language, character string }
	seen := make(map[ruleKey]struct{}, len(rules))
	lower := cases.Lower(language.Und)
	for _, rule := range rules {
		code, err := normalize(rule.LanguageCode)
		if err != nil {
			return nil, err
		}
		if !utf8.ValidString(rule.Character) {
			return nil, failure(400, invalidSlugRulesCode)
		}
		character := norm.NFC.String(lower.String(norm.NFC.String(rule.Character)))
		r, size := utf8.DecodeRuneInString(character)
		if r <= unicode.MaxASCII || size != len(character) || !unicode.IsGraphic(r) || unicode.IsSpace(r) || !validSlugReplacement(rule.Replacement) {
			return nil, failure(400, invalidSlugRulesCode)
		}
		key := ruleKey{code, character}
		if _, duplicate := seen[key]; duplicate {
			return nil, failure(400, invalidSlugRulesCode)
		}
		seen[key] = struct{}{}
		result = append(result, SlugRule{LanguageCode: code, Character: character, Replacement: rule.Replacement})
	}
	return result, nil
}

func validSlugReplacement(value string) bool {
	if len(value) > maxSlugReplacementBytes {
		return false
	}
	for _, r := range value {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func readSlugRules(ctx context.Context, db siteQueryer, query string, args ...any) ([]SlugRule, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	rules := []SlugRule{}
	for rows.Next() {
		var rule SlugRule
		if err := rows.Scan(&rule.LanguageCode, &rule.Character, &rule.Replacement); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func storeSlugRules(ctx context.Context, tx *sql.Tx, revision string, rules []SlugRule) error {
	if rules == nil {
		// The caller still holds the singleton lock and has not replaced its draft pointer.
		_, err := tx.ExecContext(ctx, `INSERT INTO public.site_revision_slug_rules(revision_id,language_code,source_character,replacement)
 SELECT $1::uuid,r.language_code,r.source_character,r.replacement FROM public.site_state s
 JOIN public.site_revision_slug_rules r ON r.revision_id=COALESCE(s.draft_id,s.published_id)
 WHERE s.singleton
 UNION ALL
 SELECT $1::uuid,i.language_code,i.source_character,i.replacement FROM public.initial_slug_rules i
 CROSS JOIN public.site_state s WHERE s.singleton AND s.draft_id IS NULL AND s.published_id IS NULL`, revision)
		return dbError(err)
	}
	if len(rules) == 0 {
		return nil
	}
	languages := make([]string, 0, len(rules))
	characters := make([]string, 0, len(rules))
	replacements := make([]string, 0, len(rules))
	for _, rule := range rules {
		languages = append(languages, rule.LanguageCode)
		characters = append(characters, rule.Character)
		replacements = append(replacements, rule.Replacement)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO public.site_revision_slug_rules(revision_id,language_code,source_character,replacement)
 SELECT $1,language_code,source_character,replacement FROM unnest($2::text[],$3::text[],$4::text[])
 AS rules(language_code,source_character,replacement)`, revision, languages, characters, replacements)
	return dbError(err)
}
