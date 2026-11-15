package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/publishing"
)

const firstHTTPSlug = "http-first"

type httpTestDatabase struct{ *sql.DB }

func (db httpTestDatabase) Ping(ctx context.Context) error { return db.PingContext(ctx) }

func publicRequest(t *testing.T, ctx context.Context, client *http.Client, target string, status int) ([]byte, http.Header) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	requireNoError(t, err)
	response, err := client.Do(request)
	requireNoError(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	requireNoError(t, err)
	if response.StatusCode != status {
		t.Fatalf("GET %s: got %d, want %d; body=%s", target, response.StatusCode, status, body)
	}
	if response.Header.Get("X-Request-ID") == "" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("request ID or cache policy missing")
	}
	return body, response.Header
}

func assertHTTPArticles(t *testing.T, body []byte, ids ...string) {
	t.Helper()
	var articles []struct {
		ID string `json:"article_id"`
	}
	requireNoError(t, json.Unmarshal(body, &articles))
	actual := make([]string, 0, len(articles))
	for _, article := range articles {
		actual = append(actual, article.ID)
	}
	if articles == nil || !slices.Equal(actual, ids) {
		t.Fatalf("HTTP list got %s, want article IDs %v", body, ids)
	}
}

func assertHTTPPublishedTranslations(t *testing.T, body []byte, expected ...string) {
	t.Helper()
	var article struct {
		PublishedTranslations []struct {
			LanguageCode string `json:"language_code"`
			Slug         string `json:"slug"`
		} `json:"published_translations"`
	}
	requireNoError(t, json.Unmarshal(body, &article))
	actual := make([]string, 0, len(article.PublishedTranslations))
	for _, translation := range article.PublishedTranslations {
		actual = append(actual, translation.LanguageCode+":"+translation.Slug)
	}
	if article.PublishedTranslations == nil || !slices.Equal(actual, expected) {
		t.Fatalf("published translations got %s, want %v", body, expected)
	}
}

func (f applicationFixture) testPublicHTTP(t *testing.T, ctx context.Context) {
	server := httptest.NewServer(platform.Handler(httpTestDatabase{f.runtime}, f.reader, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer server.Close()
	client := publicHTTPClient(t)
	f.testHTTPPublishing(t, ctx, client, server.URL)
	f.testHTTPFailures(t, ctx, client, server.URL)
}

func publicHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

// Один сценарий проверяет и встроенный transport, и собранную цепочку через Caddy.
func (f applicationFixture) testHTTPPublishing(t *testing.T, ctx context.Context, client *http.Client, base string) {
	_, err := f.owner.ExecContext(ctx, "UPDATE public.languages SET is_enabled=(code<>'pl') WHERE code IN ('kk','pl')")
	requireNoError(t, err)
	languages, _ := publicRequest(t, ctx, client, base+"/api/v1/languages", http.StatusOK)
	var catalog []struct {
		Code string `json:"code"`
	}
	requireNoError(t, json.Unmarshal(languages, &catalog))
	codes := make([]string, 0, len(catalog))
	for _, language := range catalog {
		codes = append(codes, language.Code)
	}
	if !slices.Contains(codes, "kk") || slices.Contains(codes, "pl") {
		t.Fatal("HTTP language catalog lost enabled or exposed disabled language")
	}
	empty, _ := publicRequest(t, ctx, client, base+"/api/v1/articles?lang=kk", http.StatusOK)
	assertHTTPArticles(t, empty)
	first := publishApplicationDraft(t, ctx, f, map[string]publishing.TranslationPatch{"kk": completeTranslation(firstHTTPSlug)})
	second := publishApplicationDraft(t, ctx, f, map[string]publishing.TranslationPatch{"kk": completeTranslation("http-second")})
	_ = createApplicationDraft(t, ctx, f, map[string]publishing.TranslationPatch{"kk": completeTranslation("http-private")})
	list, _ := publicRequest(t, ctx, client, base+"/api/v1/articles?lang=kk", http.StatusOK)
	assertHTTPArticles(t, list, second.ID, first.ID)
	page, _ := publicRequest(t, ctx, client, base+"/api/v1/articles?lang=kk&limit=1&offset=1", http.StatusOK)
	assertHTTPArticles(t, page, first.ID)
	f.testHTTPAliases(t, ctx, client, base, first)
}

func (f applicationFixture) testHTTPAliases(t *testing.T, ctx context.Context, client *http.Client, base string, state publishing.ArticleState) {
	const middleSlug = "http-middle"
	const finalSlug = "http-final"
	for _, slug := range []string{middleSlug, finalSlug} {
		next, err := f.editor.SaveDraft(ctx, state.ID, state.Version, map[string]publishing.TranslationPatch{"kk": completeTranslation(slug)})
		requireNoError(t, err)
		next = readyApplicationDraft(t, ctx, f, next, "kk")
		state, err = f.editor.Publish(ctx, next.ID, next.Version)
		requireNoError(t, err)
	}
	for _, old := range []string{firstHTTPSlug, middleSlug} {
		body, headers := publicRequest(t, ctx, client, base+"/api/v1/articles/"+old+"?lang=KK", http.StatusMovedPermanently)
		if strings.TrimSpace(string(body)) != "null" || !strings.HasPrefix(headers.Get("Content-Type"), "application/json") {
			t.Fatal("redirect body differs from its OpenAPI contract")
		}
		if headers.Get("Location") != "/api/v1/articles/http-final?lang=kk" {
			t.Fatal("alias did not redirect directly to safe same-language canonical URL")
		}
	}
	body, _ := publicRequest(t, ctx, client, base+"/api/v1/articles/http-final?lang=kk", http.StatusOK)
	var article map[string]any
	requireNoError(t, json.Unmarshal(body, &article))
	if article["article_id"] != state.ID || article["slug"] != finalSlug || article["language_code"] != "kk" || len(article) != 6 {
		t.Fatalf("unexpected public article payload %s", body)
	}
	assertHTTPPublishedTranslations(t, body, "kk:"+finalSlug)
	publicRequest(t, ctx, client, base+"/api/v1/articles/http-final?lang=en", http.StatusNotFound)
	testLiveSDK(t, ctx, base)
	_, err := f.editor.UnpublishArticle(ctx, state.ID, state.Version)
	requireNoError(t, err)
	for _, slug := range []string{firstHTTPSlug, middleSlug, finalSlug} {
		publicRequest(t, ctx, client, base+"/api/v1/articles/"+slug+"?lang=kk", http.StatusNotFound)
	}
}

// Генерируемый Fetch-клиент обращается к тому же HTTP-серверу и runtime-роли.
func testLiveSDK(t *testing.T, ctx context.Context, base string) {
	t.Helper()
	command := exec.CommandContext(ctx, "node", "../../../../packages/sdk/tests/live.mjs")
	command.Env = append(os.Environ(), "STAGE14_API_URL="+base,
		"STAGE14_ARTICLE_SLUG=http-final", "STAGE14_REDIRECT_SLUG=http-first", "STAGE14_ARTICLE_LANG=kk")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live generated SDK failed: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

func (f applicationFixture) testHTTPFailures(t *testing.T, ctx context.Context, client *http.Client, base string) {
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/api/v1/articles", 400}, {"/api/v1/articles?lang=en_US", 400},
		{"/api/v1/articles?lang=kk&limit=0", 400}, {"/api/v1/articles?lang=kk&limit=101", 400},
		{"/api/v1/articles?lang=kk&offset=-1", 400}, {"/api/v1/articles?lang=kk&offset=text", 400},
		{"/api/v1/articles?lang=kk&limit=", 400}, {"/api/v1/articles?lang=kk&lang=en", 400},
		{"/api/v1/articles?lang=kk&extra=1", 400}, {"/api/v1/articles?lang=%zz", 400},
		{"/api/v1/articles/invalid_slug?lang=kk", 400}, {"/api/v1/articles?lang=pl", 404},
		{"/api/v1/articles?lang=fr", 404}, {"/api/v1/articles/http-private?lang=kk", 404},
		{"/health/live", 200}, {"/health/ready", 200}, {"/docs", 404}, {"/openapi.json", 404},
	} {
		body, headers := publicRequest(t, ctx, client, base+test.path, test.status)
		if test.status == 400 || test.status == 404 {
			assertSafeProblem(t, body, headers, test.status)
		}
	}
	var private string
	err := f.runtime.QueryRowContext(ctx, "SELECT id::text FROM public.article_revisions LIMIT 1").Scan(&private)
	assertSQLState(t, err, "42501")
	requireNoError(t, f.runtime.Close())
	for _, path := range []string{"/api/v1/languages", "/api/v1/articles?lang=kk", "/api/v1/articles/http-second?lang=kk", "/health/ready"} {
		body, headers := publicRequest(t, ctx, client, base+path, http.StatusServiceUnavailable)
		assertSafeProblem(t, body, headers, http.StatusServiceUnavailable)
	}
	publicRequest(t, ctx, client, base+"/health/live", http.StatusOK)
}

func assertSafeProblem(t *testing.T, body []byte, headers http.Header, status int) {
	t.Helper()
	var problem struct {
		Status int `json:"status"`
	}
	requireNoError(t, json.Unmarshal(body, &problem))
	if problem.Status != status || !strings.HasPrefix(headers.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("invalid problem response %s", body)
	}
	for _, private := range []string{"SELECT ", "stage12_", "postgres://", "sql:", publisherTestRole, "database is closed"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("private dependency details leaked: %s", body)
		}
	}
}
