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

// TestSiteRuntimeFixture публикует только синтетические тексты в помеченной БД
// runtime harness; это не seed и не операторский интерфейс установки.
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
	draft := runtimeSiteDraft(mode)
	editor := siteconfig.NewService(applicationDatabase(t, cfg, publisherTestRole, "site-runtime-publisher"))
	version, err := editor.SaveDraft(ctx, version, draft)
	requireNoError(t, err)
	if mode != runtimeDraft {
		_, _, err = editor.Publish(ctx, version)
		requireNoError(t, err)
	}
}

func runtimeSiteDraft(mode string) siteconfig.Draft {
	draft := siteFixture()
	draft.Settings.Title = "Fixture secondary"
	if mode == runtimePrimary {
		draft.Settings.Title = "Fixture primary"
		draft.Settings.DefaultLanguage = "ru"
		draft.Settings.FallbackLanguage = "ru"
		delete(draft.Catalogs, "pl")
		draft.Catalogs["ru"] = map[string]string{}
		for key := range draft.Catalogs["en"] {
			draft.Catalogs["ru"][key] = "Fixture ru " + key
		}
	}
	if mode == "updated" {
		draft.Settings.Title = "Fixture updated"
		draft.Settings.DefaultLanguage = "en"
		draft.Catalogs["fr"] = map[string]string{}
		for key := range draft.Catalogs["en"] {
			draft.Catalogs["fr"][key] = "Fixture fr " + key
		}
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
