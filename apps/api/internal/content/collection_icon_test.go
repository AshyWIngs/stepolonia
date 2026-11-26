package content

import (
	"strings"
	"testing"
)

func TestNormalizeCollectionIcon(t *testing.T) {
	const source = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><g stroke-linecap="round"><path d="M3 5l4-1"/><circle cx="12" cy="12" r="6"/></g></svg>`
	const expected = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"><g stroke-linecap="round"><path d="M3 5l4-1"></path><circle cx="12" cy="12" r="6"></circle></g></svg>`
	for _, input := range []string{source, expected, " \n" + source + "\t "} {
		got, err := normalizeCollectionIcon(input)
		if err != nil || got != expected {
			t.Fatalf("normalizeCollectionIcon() = %q, %v; want %q", got, err, expected)
		}
	}
	for _, input := range []string{"", " \n\t "} {
		got, err := normalizeCollectionIcon(input)
		if err != nil || got != "" {
			t.Fatalf("normalizeCollectionIcon(%q) = %q, %v; want empty icon", input, got, err)
		}
	}
}

func TestRejectUnsupportedCollectionIcon(t *testing.T) {
	insideSVG := func(element string) string { return `<svg viewBox="0 0 24 24">` + element + "</svg>" }
	const shape = `<circle cx="12" cy="12" r="6"/>`
	inputs := map[string]string{
		"oversize":            strings.Repeat(" ", MaxCollectionIconBytes+1),
		"invalid XML":         `<svg viewBox="0 0 24 24"><circle r="6"></svg>`,
		"wrong root":          `<circle r="6"/>`,
		"extra root":          insideSVG(shape) + insideSVG(shape),
		"directive":           `<!DOCTYPE svg>` + insideSVG(shape),
		"foreign namespace":   `<svg xmlns="urn:other" viewBox="0 0 24 24">` + shape + "</svg>",
		"duplicate attribute": insideSVG(`<circle r="6" r="3"/>`),
		"missing viewbox":     "<svg>" + shape + "</svg>",
		"empty drawing":       insideSVG("<g/>"),
		"negative viewbox":    `<svg viewBox="0 0 -24 24">` + shape + "</svg>",
		"unbounded radius":    insideSVG(`<circle r="100000"/>`),
		"unsupported style":   insideSVG(`<circle r="6" style="fill:red"/>`),
		"external reference":  insideSVG(`<path d="M3 5l4-1" href="/icon.svg"/>`),
		"unsupported paint":   insideSVG(`<circle r="6" fill="url(#paint)"/>`),
		"nongeometry":         insideSVG("<title>Example icon</title>" + shape),
		"excessive depth":     insideSVG(strings.Repeat("<g>", 8) + shape + strings.Repeat("</g>", 8)),
		"excessive elements":  insideSVG(strings.Repeat(shape, 64)),
	}
	for label, input := range inputs {
		t.Run(label, func(t *testing.T) {
			got, err := normalizeCollectionIcon(input)
			if err == nil || got != "" {
				t.Fatalf("rejected input returned %q with error %v", got, err)
			}
		})
	}
}
