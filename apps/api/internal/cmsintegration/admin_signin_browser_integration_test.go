package cmsintegration

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"stepolonia/apps/api/internal/platform"
)

func TestCMSAdminSignInBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	portText := os.Getenv("CMS_UI_FRONTEND_PORT")
	if portText == "" {
		t.Skip("NOT RUN: start the built Nuxt app on loopback and set CMS_UI_FRONTEND_PORT")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("CMS_UI_FRONTEND_PORT must be a valid loopback port")
	}
	web := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	f := newAuthHTTPFixtureWithHandler(t, func(auth http.Handler) http.Handler {
		return adminBrowserProxy(web, auth)
	})

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

func adminBrowserProxy(web *url.URL, auth http.Handler) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := platform.HandlerWithAuth(nil, nil, logger, auth)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(web)
			req.Out.Header.Del("Cookie")
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
