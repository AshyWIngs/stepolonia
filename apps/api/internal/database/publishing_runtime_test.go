package database

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"stepolonia/apps/api/internal/publishing"
)

// Опубликованный Markdown проверяется настоящими SSR и браузером как недоверенный текст.
const runtimeArticleMarkdown = `## Une publication

Publié **en français**, avec un [lien sûr](https://example.org).

<script>globalThis.__stepoloniaXss = 1</script>
<img src="/injected-image" onerror="globalThis.__stepoloniaXss = 2">
[JavaScript](javascript:alert(1))
[Encoded](jav&#x61;script:alert(1))
[VBScript](vbscript:msgbox(1))
[Data](data:text/html;base64,PHNjcmlwdD4=)
`

// Harness уже применил миграцию, seed и импорт. Здесь проверяется собранная
// HTTP-цепочка; проверки миграций, доменных откатов и гонок повторно не запускаются.
func TestPublishingRuntimeIntegration(t *testing.T) {
	base := os.Getenv("STAGE15_API_URL")
	if base == "" {
		t.Skip("NOT RUN: STAGE15_API_URL is required for disposable Compose")
	}
	target, err := url.Parse(base)
	if err != nil || !runtimeLoopbackURL(target) {
		t.Fatal("refusing non-loopback disposable HTTP endpoint")
	}
	port, err := strconv.Atoi(target.Port())
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("disposable HTTP endpoint requires an explicit TCP port")
	}
	// Host из окружения не попадает в запрос: harness публикует Caddy на IPv4 loopback.
	base = "http://127.0.0.1:" + strconv.Itoa(port)
	cfg := disposableDatabaseConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := applicationDatabase(t, cfg, "", "publishing-runtime-acceptance")
	requireNoError(t, owner.PingContext(ctx))
	_, err = owner.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code IN ('ru','en','fr')")
	requireNoError(t, err)
	f := newApplicationFixture(t, owner, cfg)
	client := publicHTTPClient(t)
	f.testHTTPPublishing(t, ctx, client, base)
	f.prepareImportedLanguageHTTP(t, ctx, client, base)
	writeSiteRuntimeFixture(t, ctx, cfg, runtimePrimary)
}

func runtimeLoopbackURL(target *url.URL) bool {
	if target == nil || target.Scheme != "http" || target.User != nil || target.Path != "" || target.RawQuery != "" || target.Fragment != "" {
		return false
	}
	address := net.ParseIP(target.Hostname())
	return target.Hostname() == "localhost" || (address != nil && address.IsLoopback())
}

// Оставляем импортированный пятый язык опубликованным для smoke, SDK и проверки
// восстановления соединений API после остановки disposable PostgreSQL.
func (f applicationFixture) prepareImportedLanguageHTTP(t *testing.T, ctx context.Context, client *http.Client, base string) {
	empty, _ := publicRequest(t, ctx, client, base+"/api/v1/articles?lang=fr", http.StatusOK)
	assertHTTPArticles(t, empty)
	_, err := f.owner.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code='pl'")
	requireNoError(t, err)
	state := publishApplicationDraft(t, ctx, f, map[string]publishing.TranslationPatch{
		"fr": completeTranslation("stage15-fr-first"),
		"ru": completeTranslation("stage22-ru-first"),
		"pl": completeTranslation("stage22-pl-hidden"),
	})
	_, err = f.owner.ExecContext(ctx, disablePolishSQL)
	requireNoError(t, err)
	patch := completeTranslation("stage15-fr-current")
	patch.Title = valueField("Bonjour de Stepolonia")
	patch.BodyMarkdown = valueField(runtimeArticleMarkdown)
	state, err = f.editor.SaveDraft(ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{
		"fr": patch, "ru": completeTranslation("stage22-ru-current"), "kk": completeTranslation("stage22-kk-private"),
	})
	requireNoError(t, err)
	state = readyApplicationDraft(t, ctx, f, state, "fr", "ru")
	state, err = f.editor.Publish(ctx, state.ID, state.Version)
	requireNoError(t, err)
	_, err = f.editor.SaveDraft(ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{"ru": completeTranslation("stage22-ru-draft")})
	requireNoError(t, err)
	list, _ := publicRequest(t, ctx, client, base+"/api/v1/articles?lang=fr", http.StatusOK)
	assertHTTPArticles(t, list, state.ID)
	for _, address := range []struct{ language, slug string }{{"fr", "stage15-fr-current"}, {"ru", "stage22-ru-current"}} {
		body, _ := publicRequest(t, ctx, client, base+"/api/v1/articles/"+address.slug+"?lang="+address.language, http.StatusOK)
		assertHTTPPublishedTranslations(t, body, "fr:stage15-fr-current", "ru:stage22-ru-current")
	}
	for _, address := range []struct{ language, slug string }{{"pl", "stage22-pl-hidden"}, {"kk", "stage22-kk-private"}, {"ru", "stage22-ru-draft"}} {
		publicRequest(t, ctx, client, base+"/api/v1/articles/"+address.slug+"?lang="+address.language, http.StatusNotFound)
	}
}
