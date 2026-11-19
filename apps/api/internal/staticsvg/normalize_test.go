package staticsvg

import (
	"strings"
	"testing"
)

func TestNormalizeDropsComments(t *testing.T) {
	const drawing = `<svg viewBox="0 0 30 20"><rect width="30" height="20" fill="#ffffff"/></svg>`
	withComment := strings.Replace(drawing, "<rect ", "<!-- Flag background --><rect ", 1)
	expected, err := Normalize(drawing)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Normalize(withComment); err != nil || got != expected {
		t.Fatalf("comment changed the normalized drawing: %v", err)
	}
}

func TestNormalizeCalendarIcon(t *testing.T) {
	input := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
  <rect x="3" y="4" width="18" height="18" rx="2" ry="2" />
  <line x1="8" y1="2" x2="8" y2="6" />
  <line x1="16" y1="2" x2="16" y2="6" />
  <line x1="3" y1="10" x2="21" y2="10" />
  <rect x="8" y="14" width="3" height="3" rx="0.5" fill="currentColor" stroke="none" />
</svg>`
	normalized, err := Normalize(input)
	if err != nil || !strings.Contains(normalized, `rx="0.5" fill="currentColor" stroke="none"`) {
		t.Fatalf("calendar SVG was not normalized: %v", err)
	}
	short := strings.Replace(input, svgNamespace, shortSVGNamespace, 1)
	if got, err := Normalize(short); err != nil || got != normalized {
		t.Fatalf("short namespace did not normalize to the same SVG: %v", err)
	}
	foreign := strings.Replace(input, svgNamespace, "urn:example:svg", 1)
	if _, err := Normalize(foreign); err == nil {
		t.Fatal("foreign SVG namespace accepted")
	}
}

func TestPathGeometry(t *testing.T) {
	for _, valid := range []string{
		"M3 5l4-1",
		"M10.5.5L12,16 16,12Z",
		"m1e1,1E+1h3v4a5 5 0 0110 10z",
		"M0 0 C3 3 5 8 10 10S12 12 16 8 Q18 4 22 12 T24 24z",
	} {
		if !validIconPath(valid) {
			t.Errorf("valid path %q rejected", valid)
		}
	}
	for _, invalid := range []string{
		"M0 0 L", "M0 0 L0", "M0 0 H", "M0 0Z5",
		"M0 0 A1 1 0 2 0 8 8", "M0 0 A-1 1 0 1 0 8 8",
		"M0 0,", "M,0 0", "M0,,0", "M0 0 R3 4", "M0 0 H9000",
		"M0 0 H" + strings.Repeat("1 ", maxIconCoordinates),
	} {
		if validIconPath(invalid) {
			t.Errorf("invalid path %q accepted", invalid)
		}
	}
}
