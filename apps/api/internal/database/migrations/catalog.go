// Package migrations owns the filesystem supplied to the Goose migration provider.
package migrations

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql ui_catalogs/*.txt
var sources embed.FS

func Provider(db *sql.DB) (*goose.Provider, error) {
	files, err := withInitialUICatalog(sources)
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, files)
}

const (
	initialSettingsMigration = "00007_initial_site_settings.sql"
	initialCatalogMarker     = "<embedded UI catalog>"
)

func withInitialUICatalog(files fs.FS) (fs.FS, error) {
	template, err := fs.ReadFile(files, initialSettingsMigration)
	if err != nil {
		return nil, err
	}
	if strings.Count(string(template), initialCatalogMarker) != 1 {
		return nil, fmt.Errorf("initial settings migration requires one embedded UI catalog marker")
	}

	catalog, err := readInitialUICatalog(files)
	if err != nil {
		return nil, err
	}
	info, err := fs.Stat(files, initialSettingsMigration)
	if err != nil {
		return nil, err
	}
	settings := strings.Replace(string(template), initialCatalogMarker, catalog, 1)
	return catalogMigrationFS{
		FS:       files,
		settings: settings,
		info:     catalogMigrationInfo{FileInfo: info, size: int64(len(settings))},
	}, nil
}

func readInitialUICatalog(files fs.FS) (string, error) {
	paths, err := fs.Glob(files, "ui_catalogs/*.txt")
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("initial UI catalog files are missing")
	}
	fragments := make([]string, 0, len(paths))
	for _, path := range paths {
		data, err := fs.ReadFile(files, path)
		if err != nil {
			return "", err
		}
		fragment := strings.Trim(string(data), "\n")
		if strings.TrimSpace(fragment) == "" || hasMigrationDelimiters(fragment) {
			return "", fmt.Errorf("invalid initial UI catalog file %s", path)
		}
		fragments = append(fragments, fragment)
	}
	return strings.Join(fragments, "\n\n"), nil
}

func hasMigrationDelimiters(fragment string) bool {
	if strings.Contains(fragment, "$catalog$") || strings.Contains(fragment, "$$") {
		return true
	}
	for line := range strings.SplitSeq(fragment, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") && strings.Contains(line, "+goose") {
			return true
		}
	}
	return false
}

// Goose parses the seed template with its embedded catalogs as a single SQL migration.
type catalogMigrationFS struct {
	fs.FS
	settings string
	info     fs.FileInfo
}

func (files catalogMigrationFS) Open(name string) (fs.File, error) {
	if name == initialSettingsMigration {
		return catalogMigrationFile{
			Reader: strings.NewReader(files.settings),
			info:   files.info,
		}, nil
	}
	file, err := files.FS.Open(name)
	if err != nil || name != "." {
		return file, err
	}
	if dir, ok := file.(fs.ReadDirFile); ok {
		return catalogMigrationDir{ReadDirFile: dir, info: files.info}, nil
	}
	return file, nil
}

type catalogMigrationDir struct {
	fs.ReadDirFile
	info fs.FileInfo
}

func (d catalogMigrationDir) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := d.ReadDirFile.ReadDir(n)
	for i, entry := range entries {
		if entry.Name() == initialSettingsMigration {
			entries[i] = fs.FileInfoToDirEntry(d.info)
		}
	}
	return entries, err
}

type catalogMigrationFile struct {
	*strings.Reader
	info fs.FileInfo
}

func (f catalogMigrationFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (catalogMigrationFile) Close() error                 { return nil }

type catalogMigrationInfo struct {
	fs.FileInfo
	size int64
}

func (i catalogMigrationInfo) Size() int64 { return i.size }
