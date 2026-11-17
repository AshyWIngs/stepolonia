package cmshttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"stepolonia/apps/api/internal/cms"
	"strings"
	"testing"
	"time"
)

// This opt-in fixture uses the repository's pinned Playwright and a local Chromium.
func TestBrowserCSRF(t *testing.T) {
	if os.Getenv("CMS_HTTP_BROWSER_TEST") != "1" {
		t.Skip("set CMS_HTTP_BROWSER_TEST=1 with local Playwright Chromium installed")
	}
	var auth http.Handler
	observed := make(chan http.Header, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin" {
			w.Header().Set(contentTypeHeader, "text/html")
			_, _ = w.Write([]byte("<!doctype html><title>Auth transport fixture</title>"))
			return
		}
		if r.URL.Path == authPrefix+"/csrf" {
			observed <- r.Header.Clone()
		}
		auth.ServeHTTP(w, r)
	}))
	defer server.Close()
	var err error
	auth, err = NewHandler(Config{CanonicalOrigin: server.URL}, &fakeAuth{logoutErr: cms.ErrCSRF})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "-e", browserCSRFScript)
	command.Env = append(os.Environ(), "CMS_BROWSER_ORIGIN="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Chromium CSRF fetch: %v: %s", err, output)
	}
	var headers http.Header
	select {
	case headers = <-observed:
	case <-ctx.Done():
		t.Fatal("browser request not observed")
	}
	if headers.Get(originHeader) != "" || headers.Get("Sec-Fetch-Site") != sameOriginSite || !strings.HasPrefix(headers.Get("Referer"), server.URL+"/") {
		t.Fatal("browser did not supply absent Origin and same-origin fetch metadata")
	}
}

const browserCSRFScript = `
const { chromium } = require('../../../web/node_modules/playwright');
(async () => {
 const browser = await chromium.launch({ headless: true });
 try {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  await page.goto(process.env.CMS_BROWSER_ORIGIN + '/admin');
  const result = await page.evaluate(async () => {
   const response = await fetch('/api/v1/admin/auth/csrf');
   return { status: response.status, body: await response.json() };
  });
  if (result.status !== 200 || !result.body.csrf_token) throw new Error(JSON.stringify(result));
  const cookies = await context.cookies();
  if (!cookies.some(c => c.name === '__Host-sp_preauth' && c.secure && c.httpOnly && c.sameSite === 'Strict')) throw new Error('restricted cookie missing');
  await page.goto(process.env.CMS_BROWSER_ORIGIN + '/api/v1/admin/auth/logout');
  if (!(await context.cookies()).some(c => c.name === '__Host-sp_preauth')) throw new Error('GET logout deleted cookie');
  const denied = await page.evaluate(async () => (await fetch('/api/v1/admin/auth/logout', {
   method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': 'b'.repeat(43) }, body: '{}'
  })).status);
  if (denied !== 403 || !(await context.cookies()).some(c => c.name === '__Host-sp_preauth')) throw new Error('forged CSRF logout changed cookie');

 } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
`
