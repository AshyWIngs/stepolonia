package identity

import "testing"

const roleGrantTestType = "c0000000-0000-4000-8000-000000000001"

// Independent policy fixtures preserve the expected wire operations and scopes.
const (
	roleTestReadOperation   = "roles.read"
	roleTestCreateContent   = "content.items.create"
	roleTestReadContent     = "content.items.read"
	roleTestDelegateContent = "content.permissions.delegate"
	roleTestInstallation    = "installation"
	roleTestContent         = "content"
	roleTestTypeScope       = "type"
	roleTestAssigned        = "assigned"
	roleTestAny             = "any"
)

func TestRoleGrantValidation(t *testing.T) {
	valid := []RoleGrant{
		{Operation: roleTestReadOperation, Scope: roleTestInstallation},
		{Operation: roleTestCreateContent, Scope: roleTestContent, TypeID: roleGrantTestType, ObjectScope: roleTestTypeScope},
		{Operation: roleTestReadContent, Scope: roleTestContent, TypeID: roleGrantTestType, ObjectScope: roleTestAssigned},
		{Operation: roleTestDelegateContent, Scope: roleTestInstallation, TargetScope: roleTestInstallation, ObjectCeiling: roleTestAny, DelegateOperations: []string{roleTestReadContent}},
		{Operation: roleTestDelegateContent, Scope: roleTestInstallation, TypeID: roleGrantTestType, TargetScope: roleTestTypeScope, ObjectCeiling: roleTestAssigned, DelegateOperations: []string{roleTestCreateContent}},
	}
	for _, g := range valid {
		if err := g.validate(); err != nil {
			t.Fatalf("valid policy refused: %+v: %v", g, err)
		}
	}
	invalid := []RoleGrant{
		{Operation: "invented", Scope: roleTestInstallation},
		{Operation: roleTestReadOperation, Scope: roleTestContent},
		{Operation: roleTestReadOperation, Scope: roleTestInstallation, TypeID: roleGrantTestType},
		{Operation: roleTestReadOperation, Scope: roleTestInstallation, MayDelegateContent: true},
		{Operation: roleTestReadContent, Scope: roleTestContent, TypeID: "malformed", ObjectScope: roleTestAny},
		{Operation: roleTestReadContent, Scope: roleTestContent, TypeID: roleGrantTestType, ObjectScope: roleTestTypeScope},
		{Operation: roleTestCreateContent, Scope: roleTestContent, TypeID: roleGrantTestType, ObjectScope: roleTestAny},
		{Operation: roleTestDelegateContent, Scope: roleTestInstallation, TargetScope: roleTestInstallation, ObjectCeiling: roleTestAny},
		{Operation: roleTestDelegateContent, Scope: roleTestInstallation, TargetScope: roleTestInstallation, ObjectCeiling: roleTestAny, DelegateOperations: []string{roleTestReadOperation}},
		{Operation: roleTestDelegateContent, Scope: roleTestInstallation, TargetScope: roleTestInstallation, ObjectCeiling: roleTestAny, DelegateOperations: []string{roleTestReadContent, roleTestReadContent}},
	}
	for _, g := range invalid {
		if err := g.validate(); err != ErrValidation {
			t.Fatalf("invalid policy accepted: %+v: %v", g, err)
		}
	}
}

func TestRoleSelfAccessCoverage(t *testing.T) {
	read := accessGrant{operation: roleTestReadContent, scope: roleTestContent, typeID: roleGrantTestType, objectScope: roleTestAny, fields: []string{"title"}}
	assigned := read
	assigned.objectScope = roleTestAssigned
	assigned.fields = []string{"private"}
	before := []accessGrant{read, assigned}
	combined := read
	combined.fields = []string{"title", "private"}
	if accessDoesNotGrow(before, []accessGrant{combined}) {
		t.Fatal("mixed object scopes expanded self access")
	}
	combined.objectScope = roleTestAssigned
	if !accessDoesNotGrow(before, []accessGrant{combined}) {
		t.Fatal("compatible complete coverage refused")
	}
	combined.delegable = true
	if accessDoesNotGrow(before, []accessGrant{combined}) {
		t.Fatal("self delegation grew")
	}
	authority := accessGrant{operation: opContentDelegate, scope: grantScopeInstallation, targetScope: authorityTargetInstallation, ceiling: objectScopeAny, operations: []string{roleTestReadContent}}
	if accessDoesNotGrow([]accessGrant{authority}, []accessGrant{read}) {
		t.Fatal("authority supplied implicit data access")
	}
}
