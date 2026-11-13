package database

import (
	"strings"
	"testing"
)

// Invalid rows must reject the whole file rather than import a valid prefix.
func TestReadLanguages(t *testing.T) {
	const header = "code,name,native_name,direction\n"
	rows, err := ReadLanguages(strings.NewReader(header + "fr-ca,French,Français,ltr\n"))
	if err != nil || len(rows) != 1 || rows[0].Code != "fr-ca" {
		t.Fatalf("normalization: rows=%v error=%v", rows, err)
	}
	for name, data := range map[string]string{
		"normalized duplicate":   header + "fr-ca,French,Français,ltr\nfr-CA,French,Français,ltr\n",
		"invalid second row":     header + "de,German,Deutsch,ltr\nfr,,Français,ltr\n",
		"non BCP47 separator":    header + "en_US,English,English,ltr\n",
		"invalid UTF8":           header + "de,German,Deutsch,\xff\n",
		"enabled not importable": "code,name,native_name,direction,is_enabled\nde,German,Deutsch,ltr,true\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ReadLanguages(strings.NewReader(data)); err == nil || got != nil {
				t.Fatalf("invalid file accepted: %v %v", got, err)
			}
		})
	}
}
