package cmshttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newAdminTLSServer(t *testing.T, service AuthService) *httptest.Server {
	t.Helper()
	var auth http.Handler
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { auth.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	var err error
	auth, err = NewHandler(Config{CanonicalOrigin: server.URL}, service)
	if err != nil {
		t.Fatal(err)
	}
	return server
}
