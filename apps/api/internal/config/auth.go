package config

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"stepolonia/apps/api/internal/cms"
)

// AuthenticationConfig keeps the private pool separate from public reads.
type AuthenticationConfig struct {
	Database          *pgxpool.Config
	Service           cms.AuthConfig
	Origin            string
	TrustedProxyCIDRs []string
}

// Authentication is opt-in; partial or invalid enabled configuration fails startup.
func Authentication(runtime *pgxpool.Config) (*AuthenticationConfig, error) {
	enabled := os.Getenv("CMS_AUTH_ENABLED")
	if enabled == "" || enabled == "false" {
		return nil, nil
	}
	if enabled != "true" || runtime == nil {
		return nil, fmt.Errorf("CMS_AUTH_ENABLED must be true or false")
	}
	user, path := os.Getenv("CMS_DB_USER"), os.Getenv("CMS_DB_PASSWORD_FILE")
	origin, site := os.Getenv("CMS_AUTH_ORIGIN"), os.Getenv("SITE")
	if user == "" || user == runtime.ConnConfig.User || path == "" || origin == "" || site == "" {
		return nil, fmt.Errorf("CMS requires separate CMS_DB_USER, CMS_DB_PASSWORD_FILE, CMS_AUTH_ORIGIN and SITE")
	}
	password, err := privateFile(path, 4096)
	if err != nil {
		return nil, fmt.Errorf("read private CMS database password: %w", err)
	}
	defer clear(password)
	if strings.TrimSpace(string(password)) == "" {
		return nil, fmt.Errorf("CMS database password is empty")
	}
	key, version, err := InstallationKey()
	if err != nil {
		return nil, err
	}
	db := runtime.Copy()
	db.ConnConfig.User, db.ConnConfig.Password = user, strings.TrimSpace(string(password))
	db.ConnConfig.RuntimeParams["application_name"] = "sp-cms-auth"
	db.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	db.MaxConns = 4
	var peers []string
	if raw := os.Getenv("CMS_AUTH_PROXY_CIDRS"); raw != "" {
		peers = strings.Split(raw, ",")
	}
	return &AuthenticationConfig{Database: db, Service: cms.AuthConfig{Site: site, KeyVersion: version, EncryptionKey: key}, Origin: origin, TrustedProxyCIDRs: peers}, nil
}

// InstallationKey reads the operator-owned raw AEAD key without following symlinks.
func InstallationKey() ([]byte, int, error) {
	version, err := strconv.Atoi(os.Getenv("CMS_TOTP_KEY_VERSION"))
	if err != nil || version < 1 || os.Getenv("CMS_TOTP_KEY_FILE") == "" {
		return nil, 0, fmt.Errorf("CMS_TOTP_KEY_FILE and positive CMS_TOTP_KEY_VERSION are required")
	}
	key, err := privateFile(os.Getenv("CMS_TOTP_KEY_FILE"), 32)
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, 0, fmt.Errorf("TOTP key must be a private operator-owned regular file containing exactly 32 raw bytes")
	}
	return key, version, nil
}

func privateFile(path string, maximum int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open private credential file")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect private credential file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || int64(stat.Uid) != int64(os.Geteuid()) {
		return nil, fmt.Errorf("credential file must be private, regular and owned by the current process user")
	}
	data, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		clear(data)
		return nil, fmt.Errorf("cannot read bounded credential file")
	}
	return data, nil
}
