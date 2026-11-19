package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"stepolonia/apps/api/internal/siteconfig"
)

const (
	runtimePrimary = "primary"
	runtimeDraft   = "draft"
)

// TestSiteRuntimeFixture publishes synthetic text only in a marked disposable
// database owned by the runtime test harness.
func TestSiteRuntimeFixture(t *testing.T) {
	mode := os.Getenv("STAGE22_SITE_FIXTURE")
	if mode == "" {
		t.Skip("NOT RUN: STAGE22_SITE_FIXTURE is required")
	}
	cfg := disposableDatabaseConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	writeSiteRuntimeFixture(t, ctx, cfg, mode)
}

func writeSiteRuntimeFixture(t *testing.T, ctx context.Context, cfg *pgx.ConnConfig, mode string) {
	t.Helper()
	if mode != runtimePrimary && mode != "secondary" && mode != "updated" && mode != runtimeDraft {
		t.Fatal("unknown site runtime fixture")
	}
	owner := applicationDatabase(t, cfg, "", "site-runtime-fixture")
	var marker string
	requireNoError(t, owner.QueryRowContext(ctx, "SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=current_database()").Scan(&marker))
	token := os.Getenv(disposableTokenEnv)
	if token == "" || marker != "stepolonia:disposable:"+token {
		t.Fatal("refusing unmarked disposable database")
	}
	var version int64
	requireNoError(t, owner.QueryRowContext(ctx, "SELECT version FROM public.site_state WHERE singleton").Scan(&version))
	draft := runtimeSiteDraft(registeredFixtureSignatures(t, ctx, owner), mode)
	editor := siteconfig.NewService(applicationDatabase(t, cfg, publisherTestRole, "site-runtime-publisher"))
	version, err := editor.SaveDraft(ctx, version, draft)
	requireNoError(t, err)
	if mode != runtimeDraft {
		_, _, err = editor.Publish(ctx, version)
		requireNoError(t, err)
	}
}

func runtimeSiteDraft(signatures map[string][]string, mode string) siteconfig.Draft {
	draft := siteFixture(signatures)
	draft.Settings.Title = "Fixture secondary"
	if mode == runtimePrimary {
		draft.Settings.Title = "Fixture primary"
		draft.Settings.DefaultLanguage = "ru"
		draft.Settings.FallbackLanguage = "ru"
		delete(draft.Catalogs, "pl")
		draft.Catalogs["ru"] = fixtureMessages("ru", signatures)
	}
	if mode == "updated" {
		draft.Settings.Title = "Fixture updated"
		draft.Settings.DefaultLanguage = "en"
		draft.Catalogs["fr"] = fixtureMessages("fr", signatures)
		for code, messages := range draft.Catalogs {
			messages[siteArticlesKey] = "Fixture updated " + code + " articles"
		}
		draft.Catalogs["en"][siteEmptyKey] = "Fixture updated en empty @ | <b>literal</b>"
	}
	if mode == runtimeDraft {
		for code, messages := range draft.Catalogs {
			messages[siteArticlesKey] = "Private draft " + code + " articles"
			delete(messages, siteEmptyKey)
		}
	}
	return draft
}
