package content

import "testing"

func TestLegacyPublicationSlugHonorsMinimumLength(t *testing.T) {
	const itemID = "badfa6b5-284f-4661-acca-1d09e103e6dc"
	const compactID = "badfa6b5284f4661acca1d09e103e6dc"
	for _, tt := range []struct {
		name, original, expected string
		minimum                  int
	}{
		{"single character padding", "a-" + compactID, "a1", 2},
		{"neutral token", "a-" + compactID, "a-item", 6},
		{"legacy fallback", "item-" + itemID, "item-i", 6},
		{"base already fits", "swieto-" + compactID, "swieto", 6},
		{"explicit slug", "swieto-2", "swieto-2", 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := fieldConfig{Slug: true, MinLength: &tt.minimum}
			base := publicationSlugBase(tt.original, itemID, config)
			if base != tt.expected {
				t.Fatalf("canonical base %q, want %q", base, tt.expected)
			}
			if err := validateText(base, config); err != nil {
				t.Fatalf("canonical base failed schema validation: %v", err)
			}
		})
	}
}
