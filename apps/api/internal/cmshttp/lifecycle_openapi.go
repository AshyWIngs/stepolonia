package cmshttp

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/cms"
)

func isUserRead(rt route) bool {
	return authTag(rt) == usersTag && rt.method == http.MethodGet
}

func addLifecycleSchemas(schemas map[string]*huma.Schema) {
	user := safeUserResponse{ID: syntheticUserID, Login: "docs-editor", State: "disabled", Version: 3}
	schemas[safeUserSchema] = responseDTOSchema("Safe account projection without credentials, factors, recovery codes, onboarding tokens, sessions or grants. State does not imply CMS permissions.", user)
	next := 1
	list := userListResponse{Items: []safeUserResponse{user}, NextOffset: &next}
	schema := closedObjectSchema("Bounded safe user page in stable normalized-login and UUID order. Installation-wide users.read authorizes all safe projections; private security fields are never materialized. next_offset is null at the end.", map[string]*huma.Schema{
		"items":       {Type: "array", Description: "Safe account projections in stable normalized-login and UUID order.", Items: schemaRef(safeUserSchema), MaxItems: intPointer(cms.MaxUserPageLimit)},
		"next_offset": {Type: huma.TypeInteger, Nullable: true, Minimum: floatPointer(0), Maximum: floatPointer(float64(cms.MaxUserPageOffset)), Description: "Next page offset; null when no further accounts remain."},
	}, []string{"items", "next_offset"}, list)
	schema.Examples = append(schema.Examples, userListResponse{Items: []safeUserResponse{}, NextOffset: nil})
	schemas[userListSchema] = schema
}

func userPageParameters() []*huma.Param {
	return []*huma.Param{
		{Name: "limit", In: "query", Description: "Single unsigned decimal page size from 1 to 100; defaults to 20. Unknown or repeated parameters are rejected.", Schema: &huma.Schema{Type: huma.TypeInteger, Minimum: floatPointer(1), Maximum: floatPointer(float64(cms.MaxUserPageLimit)), Default: cms.DefaultUserPageLimit, Examples: []any{20}}},
		{Name: "offset", In: "query", Description: "Single unsigned decimal offset from 0 to 100; defaults to 0. Apply next_offset to read the following page.", Schema: &huma.Schema{Type: huma.TypeInteger, Minimum: floatPointer(0), Maximum: floatPointer(float64(cms.MaxUserPageOffset)), Default: 0, Examples: []any{0}}},
	}
}

func isLifecycleOperation(operation string) bool {
	return operation == listUsersOperation || operation == getUserOperation || operation == activateUserOperation || operation == disableUserOperation
}

func lifecycleSuccessDescription(rt route) string {
	switch rt.operationID {
	case listUsersOperation:
		return "Safe account page returned after full-session and users.read authorization; next_offset is null at the end."
	case getUserOperation:
		return "Safe account projection returned after full-session and users.read authorization."
	case activateUserOperation:
		return "Enrolled disabled account activated and version incremented after delegation, no-self-growth and atomic audit checks; memberships unchanged."
	default:
		return "Account disabled and version incremented; sessions and restricted attempts revoked in the same audited transaction."
	}
}
