package adminhttp

import (
	"net/http/httptest"
	"testing"
)

func newAdminTLSServer(t *testing.T, service AuthService) *httptest.Server {
	t.Helper()
	server := newAdminServer(t, service)
	server.StartTLS()
	return server
}

func newAdminServer(t *testing.T, service AuthService) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	origin := "https://" + server.Listener.Addr().String()
	auth, err := NewHandler(Config{CanonicalOrigin: origin}, service)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = auth
	return server
}
