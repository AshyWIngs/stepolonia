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

func TestLargeSelfPolicyCoverage(t *testing.T) {
	fields := make([]string, 64)
	for i := range fields {
		fields[i] = strconv.Itoa(i)
	}
	read := accessGrant{operation: roleTestReadContent, scope: grantScopeContent,
		typeID: roleGrantTestType, objectScope: objectScopeAny, fields: fields}
	before := []accessGrant{read}
	if !accessDoesNotGrow(before, []accessGrant{read}) {
		t.Fatal("complete large nondelegable source refused unchanged self access")
	}
	additional := read
	additional.fields = append(append([]string{}, fields...), "64")
	if accessDoesNotGrow(before, []accessGrant{additional}) {
		t.Fatal("large self policy acquired an additional field")
	}
	delegable := read
	delegable.delegable = true
	if accessDoesNotGrow(before, []accessGrant{delegable}) {
		t.Fatal("large self policy acquired delegation")
	}
}
