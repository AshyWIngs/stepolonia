package cms

import (
	"strconv"
	"testing"
)

func TestLargeFieldCoveragePreservesMembership(t *testing.T) {
	source := make([]string, 64)
	for i := range source {
		source[len(source)-1-i] = strconv.Itoa(i)
	}
	for _, requested := range [][]string{nil, {"17"}, {"0", "17", "63", "17"}} {
		if !coversFields(source, requested) {
			t.Fatal("unordered or repeated fields lost complete membership coverage")
		}
	}
	if coversFields(source, []string{"0", "64"}) {
		t.Fatal("large source covered an absent field")
	}
}
