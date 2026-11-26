package siteconfig

import (
	"context"
	"database/sql"
	"slices"
	"strings"
)

const InvalidLocaleDefaultsCode = "invalid_locale_defaults"
const InvalidSlugRulesCode = invalidSlugRulesCode

type LocaleDefaults struct {
	DefaultLanguage  string `json:"default_language" minLength:"2" maxLength:"63"`
	FallbackLanguage string `json:"fallback_language" minLength:"2" maxLength:"63"`
}

func (s Settings) LocaleDefaults() LocaleDefaults {
	return LocaleDefaults{s.DefaultLanguage, s.FallbackLanguage}
}

func (s LocaleDefaults) Version() string { return settingsScopeVersion(s) }

func ApplyLocaleDefaultsTx(ctx context.Context, tx *sql.Tx, expected string, defaults LocaleDefaults) (ScopedPublication, error) {
	content, contentErr := normalize(defaults.DefaultLanguage)
	ui, uiErr := normalize(defaults.FallbackLanguage)
	if contentErr != nil || uiErr != nil || content != defaults.DefaultLanguage || ui != defaults.FallbackLanguage {
		return ScopedPublication{}, failure(400, InvalidLocaleDefaultsCode)
	}
	state, err := lockPublishedSettings(ctx, tx)
	if err != nil {
		return ScopedPublication{}, err
	}
	if state.settings.LocaleDefaults().Version() != expected {
		return ScopedPublication{}, failure(409, versionConflictCode)
	}
	var available bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.languages WHERE code=$1 AND is_enabled)
AND EXISTS(SELECT 1 FROM public.site_revision_languages WHERE revision_id=$2 AND language_code=$3)`, content, state.id, ui).Scan(&available)
	if err != nil {
		return ScopedPublication{}, dbError(err)
	}
	if !available {
		return ScopedPublication{}, failure(400, InvalidLocaleDefaultsCode)
	}
	state.settings.DefaultLanguage = content
	state.settings.FallbackLanguage = ui
	id, err := createScopedRevision(ctx, tx, state, true)
	if err != nil {
		return ScopedPublication{}, err
	}
	return activateScopedRevision(ctx, tx, state, id, defaults.Version())
}

type SlugRuleKey struct {
	LanguageCode string `json:"language_code" minLength:"2" maxLength:"63"`
	Character    string `json:"character" minLength:"1" maxLength:"4"`
}

type SlugRuleChanges struct {
	Upsert []SlugRule
	Remove []SlugRuleKey
}

func slugKey(rule SlugRule) SlugRuleKey {
	return SlugRuleKey{rule.LanguageCode, rule.Character}
}

func SlugRulesVersion(rules []SlugRule) string {
	sorted := slices.Clone(rules)
	slices.SortFunc(sorted, func(a, b SlugRule) int {
		if a.LanguageCode != b.LanguageCode {
			return strings.Compare(a.LanguageCode, b.LanguageCode)
		}
		return strings.Compare(a.Character, b.Character)
	})
	return settingsScopeVersion(sorted)
}

func ApplySlugRuleChangesTx(ctx context.Context, tx *sql.Tx, expected string, change SlugRuleChanges) (ScopedPublication, error) {
	state, err := lockPublishedSettings(ctx, tx)
	if err != nil {
		return ScopedPublication{}, err
	}
	rules, err := readSlugRules(ctx, tx, editableSlugRulesQuery, state.id)
	if err != nil {
		return ScopedPublication{}, dbError(err)
	}
	if SlugRulesVersion(rules) != expected {
		return ScopedPublication{}, failure(409, versionConflictCode)
	}
	next, err := mergeSlugRules(rules, change)
	if err != nil {
		return ScopedPublication{}, err
	}
	id, err := createScopedRevision(ctx, tx, state, false)
	if err != nil {
		return ScopedPublication{}, err
	}
	if err := storeSlugRules(ctx, tx, id, next); err != nil {
		return ScopedPublication{}, err
	}
	return activateScopedRevision(ctx, tx, state, id, SlugRulesVersion(next))
}

func mergeSlugRules(current []SlugRule, change SlugRuleChanges) ([]SlugRule, error) {
	if len(change.Remove)+len(change.Upsert) == 0 {
		return nil, failure(400, InvalidSlugRulesCode)
	}
	upsert, err := normalizeSlugRules(change.Upsert)
	if err != nil {
		return nil, failure(400, InvalidSlugRulesCode)
	}
	remove := make([]SlugRule, len(change.Remove))
	for i, key := range change.Remove {
		remove[i] = SlugRule{LanguageCode: key.LanguageCode, Character: key.Character}
	}
	remove, err = normalizeSlugRules(remove)
	if err != nil {
		return nil, failure(400, InvalidSlugRulesCode)
	}
	return applyRuleDelta(current, upsert, remove)
}

func applyRuleDelta(current, upsert, remove []SlugRule) ([]SlugRule, error) {
	rules := make(map[SlugRuleKey]SlugRule, len(current))
	removed := make(map[SlugRuleKey]bool, len(remove))
	for _, rule := range current {
		rules[slugKey(rule)] = rule
	}
	for _, rule := range remove {
		key := slugKey(rule)
		if _, exists := rules[key]; !exists {
			return nil, failure(400, InvalidSlugRulesCode)
		}
		delete(rules, key)
		removed[key] = true
	}
	for _, rule := range upsert {
		key := slugKey(rule)
		if removed[key] {
			return nil, failure(400, InvalidSlugRulesCode)
		}
		rules[key] = rule
	}
	if len(rules) > MaxSlugRules {
		return nil, failure(400, InvalidSlugRulesCode)
	}
	next := make([]SlugRule, 0, len(rules))
	for _, rule := range rules {
		next = append(next, rule)
	}
	return next, nil
}
