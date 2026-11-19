package staticsvg

import (
	"strings"
	"testing"
)

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
