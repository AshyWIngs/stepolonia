package siteconfig

import (
	"context"
	"database/sql"
	"strings"
)

const InvalidDetailsCode = "invalid_portal_details"

func ApplyPortalDetailsTx(ctx context.Context, tx *sql.Tx, expected string, details PortalDetails) (ScopedPublication, error) {
	if strings.TrimSpace(details.Title) == "" {
		return ScopedPublication{}, failure(400, InvalidDetailsCode)
	}
	state, err := lockPublishedSettings(ctx, tx)
	if err != nil {
		return ScopedPublication{}, err
	}
	if state.settings.Details().Version() != expected {
		return ScopedPublication{}, failure(409, versionConflictCode)
	}
	state.settings.Title = details.Title
	state.settings.Tagline = details.Tagline
	state.settings.Description = details.Description
	state.settings.Notice = details.Notice
	result, err := publishSettingsChange(ctx, tx, state, "", "")
	result.ScopeVersion = details.Version()
	return result, err
}
