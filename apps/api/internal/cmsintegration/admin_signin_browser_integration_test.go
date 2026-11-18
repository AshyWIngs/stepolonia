package cmsintegration

import (
	"bufio"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"stepolonia/apps/api/internal/database"
	"stepolonia/apps/api/internal/platform"
	"stepolonia/apps/api/internal/siteconfig"
)

func TestCMSAdminSignInBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	if os.Getenv("CMS_UI_BROWSER_TEST") != "1" {
		t.Skip("NOT RUN: build Nuxt and set CMS_UI_BROWSER_TEST=1")
	}
	f := newAuthHTTPFixtureWithHandler(t, func(state authFixtureState, auth http.Handler) http.Handler {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		site := siteconfig.NewReader(openPublicationRuntimeLogin(t, state.db))
		public := httptest.NewServer(platform.Handler(nil, nil, logger, site))
		t.Cleanup(public.Close)
		web := startAdminFrontend(t, public.URL)
		return adminBrowserProxy(web, platform.HandlerWithAuth(nil, nil, logger, auth, site), logger)
	})
	assertUnpublishedAdminUnavailable(t, f)
	publishAdminBrowserCatalog(t, f.state.db)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodeExecutable, "../../../web/tests/admin-signin.mjs")
	code := bootstrapCode(t, f.state.db, f.state.secret)
	command.Env = append(os.Environ(),
		"QA_CMS_ORIGIN="+f.server.URL,
		"QA_CMS_LOGIN="+bootstrapTestLogin,
		"QA_CMS_PASSWORD="+bootstrapTestPassphrase,
		"QA_CMS_CODE="+code,
	)
	output, err := command.CombinedOutput()
	diagnostic := strings.NewReplacer(bootstrapTestPassphrase, "[password]", code, "[code]").Replace(string(output))
	if err != nil {
		t.Fatalf("CMS browser verification: %v: %s", err, diagnostic)
	}
	t.Log(diagnostic)

	var active, revoked int
	requireNoError(t, f.state.db.QueryRow(`SELECT
 count(*) FILTER (WHERE revoked_at IS NULL), count(*) FILTER (WHERE revoked_at IS NOT NULL)
 FROM public.sessions WHERE kind='full'`).Scan(&active, &revoked))
	if active != 0 || revoked != 1 {
		t.Fatalf("logout storage state: active full=%d revoked full=%d", active, revoked)
	}
}

func adminBrowserProxy(web *url.URL, api http.Handler, logger *slog.Logger) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(web)
			req.Out.Header.Del("Cookie")
			if preference, err := req.In.Cookie("cms-language"); err == nil {
				req.Out.AddCookie(preference)
			}
			req.Out.Header.Del("Authorization")
		},
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			api.ServeHTTP(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func startAdminFrontend(t *testing.T, apiOrigin string) *url.URL {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, nodeExecutable, "../../../web/.output/server/index.mjs")
	command.Env = append(os.Environ(), "NITRO_HOST=127.0.0.1", "NITRO_PORT=0", "NUXT_API_ORIGIN="+apiOrigin)
	output, err := command.StdoutPipe()
	requireNoError(t, err)
	command.Stderr = io.Discard
	requireNoError(t, command.Start())
	t.Cleanup(func() {
		cancel()
		_ = command.Wait()
	})
	listening := make(chan string, 1)
	go readFrontendAddress(output, listening)
	select {
	case address := <-listening:
		web, err := url.Parse(address)
		requireNoError(t, err)
		if web.Scheme != "http" || web.Hostname() != "127.0.0.1" || web.Port() == "" || web.User != nil || web.Path != "" {
			t.Fatal("Nuxt did not announce a loopback HTTP listener")
		}
		return web
	case <-time.After(15 * time.Second):
		t.Fatal("Nuxt did not announce startup; build the app before running this test")
		return nil
	}
}

func readFrontendAddress(output io.Reader, listening chan<- string) {
	defer close(listening)
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		if address, ok := strings.CutPrefix(scanner.Text(), "Listening on "); ok {
			listening <- address
			break
		}
	}
	_, _ = io.Copy(io.Discard, output)
}

func assertUnpublishedAdminUnavailable(t *testing.T, f authHTTPFixture) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.server.URL+"/admin", nil)
	requireNoError(t, err)
	request.Header.Set("Accept", "text/html")
	response, err := f.client.Do(request)
	requireNoError(t, err)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("admin without a published catalog returned %d, want 503", response.StatusCode)
	}
}

func publishAdminBrowserCatalog(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	requireNoError(t, database.Seed(ctx, db))
	languages, err := database.ReadLanguages(strings.NewReader("code,name,native_name,direction\nfr,French,Français (rédaction QA),ltr\n"))
	requireNoError(t, err)
	requireNoError(t, database.ImportLanguages(ctx, db, languages))
	_, err = db.ExecContext(ctx, "UPDATE public.languages SET is_enabled=true WHERE code IN ('en','fr')")
	requireNoError(t, err)

	en, fr := adminBrowserCatalogs(t, db)
	service := siteconfig.NewService(db)
	version, err := service.SaveDraft(ctx, 1, siteconfig.Draft{
		Settings: siteconfig.Settings{Title: "Editorial workspace", Accent: "#205746", DefaultLanguage: "en", FallbackLanguage: "en"},
		Catalogs: map[string]map[string]string{"en": en, "fr": fr},
	})
	requireNoError(t, err)
	_, _, err = service.Publish(ctx, version)
	requireNoError(t, err)
}

func adminBrowserCatalogs(t *testing.T, db *sql.DB) (map[string]string, map[string]string) {
	t.Helper()
	enLabels := map[string]string{
		"cms.language": "Language", "cms.theme": "Theme", "cms.sign_in": "Sign in", "cms.login": "Login",
		"cms.password": "Password", "cms.continue": "Continue", "cms.two_step": "Two-step verification",
		"cms.code": "Verification code", "cms.signed_in": "Signed in", "cms.sign_out": "Sign out",
	}
	frLabels := map[string]string{"cms.language": "Langue", "cms.sign_in": "Connexion à la rédaction"}
	en, fr := map[string]string{}, map[string]string{}
	rows, err := db.Query(`SELECT message_key, array_to_string(placeholders, '} {') FROM public.ui_message_keys`)
	requireNoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var placeholders sql.NullString
		requireNoError(t, rows.Scan(&key, &placeholders))
		en[key] = catalogFixtureValue(enLabels[key], key, placeholders.String)
		fr[key] = catalogFixtureValue(frLabels[key], key, placeholders.String)
	}
	requireNoError(t, rows.Err())
	return en, fr
}

func catalogFixtureValue(label, key, placeholders string) string {
	if label == "" {
		label = "QA " + key
	}
	if placeholders != "" {
		label += " {" + placeholders + "}"
	}
	return label
}
