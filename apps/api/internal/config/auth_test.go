package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const authEnabledEnv = "CMS_AUTH_ENABLED"
const cmsDBUserEnv = "CMS_DB_USER"
const cmsKeyFileEnv = "CMS_TOTP_KEY_FILE"

func authEnvironment(t *testing.T) (*pgxpool.Config, string, string) {
	t.Helper()
	t.Setenv("CMS_DRAFT_DB_USER", "")
	t.Setenv("CMS_DRAFT_DB_PASSWORD_FILE", "")
	directory := t.TempDir()
	password, key := filepath.Join(directory, "password"), filepath.Join(directory, "key")
	for path, value := range map[string]string{password: "private-db-password", key: strings.Repeat("k", 32)} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string]string{authEnabledEnv: "true", cmsDBUserEnv: "auth_only", "CMS_DB_PASSWORD_FILE": password, "CMS_AUTH_ORIGIN": "https://example.test", cmsKeyFileEnv: key, "CMS_TOTP_KEY_VERSION": "1", "SITE": "example.test", "CMS_AUTH_PROXY_CIDRS": "127.0.0.1/32"} {
		t.Setenv(name, value)
	}
	cfg, err := pgxpool.ParseConfig("postgres://runtime@localhost/test")
	if err != nil {
		t.Fatal(err)
	}
	return cfg, password, key
}

func TestAuthenticationPoolIsolation(t *testing.T) {
	runtime, _, _ := authEnvironment(t)
	cfg, err := Authentication(runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(cfg.Service.EncryptionKey)
	if cfg.Database.ConnConfig.User != "auth_only" || cfg.Database.ConnConfig.Password != "private-db-password" || cfg.Database.ConnConfig.Database != runtime.ConnConfig.Database || runtime.ConnConfig.User != "runtime" || runtime.ConnConfig.Password != "" {
		t.Fatal("private credentials escaped into runtime configuration")
	}
	if cfg.Origin != "https://example.test" || cfg.Service.Site != "example.test" || len(cfg.Service.EncryptionKey) != 32 {
		t.Fatal("installation binding was lost")
	}
}

func TestAuthenticationConfigurationFailsClosed(t *testing.T) {
	for _, field := range []string{cmsDBUserEnv, "CMS_DB_PASSWORD_FILE", "CMS_AUTH_ORIGIN", cmsKeyFileEnv, "CMS_TOTP_KEY_VERSION", "SITE"} {
		t.Run(field, func(t *testing.T) {
			runtime, _, _ := authEnvironment(t)
			t.Setenv(field, "")
			if _, err := Authentication(runtime); err == nil {
				t.Fatal("accepted incomplete private configuration")
			}
		})
	}
	t.Run("runtime_identity", func(t *testing.T) {
		runtime, _, _ := authEnvironment(t)
		t.Setenv(cmsDBUserEnv, "runtime")
		if _, err := Authentication(runtime); err == nil {
			t.Fatal("accepted public pool identity")
		}
	})
	t.Run("disabled", func(t *testing.T) {
		runtime, _, _ := authEnvironment(t)
		t.Setenv(authEnabledEnv, strconv.FormatBool(false))
		cfg, err := Authentication(runtime)
		if cfg != nil || err != nil {
			t.Fatal("disabled CMS configured private credentials")
		}
	})
}

func TestPrivateCredentialFileSafety(t *testing.T) {
	runtime, password, key := authEnvironment(t)
	if err := os.Chmod(password, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Authentication(runtime); err == nil {
		t.Fatal("accepted readable password file")
	}
	if err := os.Chmod(password, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(key, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cmsKeyFileEnv, link)
	if _, err := Authentication(runtime); err == nil {
		t.Fatal("followed key symlink")
	}
}

func TestAuthenticationDoesNotInheritRoleSwitch(t *testing.T) {
	for _, option := range []string{"role", "session_authorization", "options"} {
		t.Run(option, func(t *testing.T) {
			runtime, _, _ := authEnvironment(t)
			runtime.ConnConfig.RuntimeParams[option] = "masked_owner"
			cfg, err := Authentication(runtime)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(cfg.Service.EncryptionKey)
			if len(cfg.Database.ConnConfig.RuntimeParams) != 2 || cfg.Database.ConnConfig.RuntimeParams[option] != "" {
				t.Fatal("private pool inherited a role-switching option")
			}
		})
	}
}

func TestDraftDatabaseConfiguration(t *testing.T) {
	const userEnv = "CMS_DRAFT_DB_USER"
	const draftLogin = "draft_writer"
	runtime, password, _ := authEnvironment(t)
	cfg, err := Authentication(runtime)
	if err != nil || cfg.DraftDatabase != nil {
		t.Fatalf("draft editing must be opt-in: %v", err)
	}
	clear(cfg.Service.EncryptionKey)
	t.Setenv(userEnv, draftLogin)
	if _, err := Authentication(runtime); err == nil {
		t.Fatal("accepted incomplete draft configuration")
	}
	t.Setenv("CMS_DRAFT_DB_PASSWORD_FILE", password)
	for _, user := range []string{runtime.ConnConfig.User, os.Getenv(cmsDBUserEnv)} {
		t.Setenv(userEnv, user)
		if _, err := Authentication(runtime); err == nil {
			t.Fatal("accepted a shared draft database identity")
		}
	}
	t.Setenv(userEnv, draftLogin)
	runtime.ConnConfig.RuntimeParams["role"] = "masked_owner"
	cfg, err = Authentication(runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(cfg.Service.EncryptionKey)
	db := cfg.DraftDatabase.ConnConfig
	if db.User != draftLogin || db.Database != runtime.ConnConfig.Database || db.RuntimeParams["role"] != "" || cfg.Database.ConnConfig.User == db.User {
		t.Fatal("draft pool is not isolated from runtime and authentication identities")
	}
	t.Setenv(authEnabledEnv, strconv.FormatBool(false))
	if _, err := Authentication(runtime); err == nil {
		t.Fatal("accepted draft configuration without CMS authentication")
	}
}
