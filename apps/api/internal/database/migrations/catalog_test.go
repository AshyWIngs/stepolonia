package migrations

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestInitialUICatalogAssembly(t *testing.T) {
	files := fstest.MapFS{
		initialSettingsMigration:   {Data: []byte("start\n" + initialCatalogMarker + "\nend\n")},
		"ui_catalogs/settings.txt": {Data: []byte("settings\n")},
		"ui_catalogs/public.txt":   {Data: []byte("public\n")},
	}
	assembled, err := withInitialUICatalog(files)
	if err != nil {
		t.Fatal(err)
	}
	if err := fstest.TestFS(assembled, initialSettingsMigration); err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(assembled, initialSettingsMigration)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "start\npublic\n\nsettings\nend\n" {
		t.Fatal("migration did not include both catalogs in path order")
	}
}

func TestInitialUICatalogRejectsBrokenSources(t *testing.T) {
	for _, input := range []struct {
		name     string
		template string
		fragment string
	}{
		{"missing marker", "start\nend", "public"},
		{"duplicate marker", strings.Repeat(initialCatalogMarker, 2), "public"},
		{"missing fragment", initialCatalogMarker, ""},
		{"whitespace fragment", initialCatalogMarker, "\n \n"},
		{"Goose annotation", initialCatalogMarker, "public\n --  +goose Down"},
		{"catalog delimiter", initialCatalogMarker, "public\n$catalog$"},
		{"SQL block delimiter", initialCatalogMarker, "public\n$$"},
	} {
		t.Run(input.name, func(t *testing.T) {
			files := fstest.MapFS{initialSettingsMigration: {Data: []byte(input.template)}}
			if input.fragment != "" {
				files["ui_catalogs/public.txt"] = &fstest.MapFile{Data: []byte(input.fragment)}
			}
			if _, err := withInitialUICatalog(files); err == nil {
				t.Fatal("invalid embedded source was accepted")
			}
		})
	}
}
