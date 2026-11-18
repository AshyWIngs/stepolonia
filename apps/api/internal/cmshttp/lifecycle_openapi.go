package cmshttp

import (
	"fmt"
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
	schema := privatePageSchema("Bounded safe user page in stable normalized-login and UUID order. Installation-wide users.read authorizes all safe projections; private security fields are never materialized. next_offset is null at the end.", safeUserSchema, cms.MaxUserPageLimit, cms.MaxUserPageOffset, list)
	schema.Examples = append(schema.Examples, userListResponse{Items: []safeUserResponse{}, NextOffset: nil})
	schemas[userListSchema] = schema
}

func userPageParameters() []*huma.Param {
	return privatePageParameters(cms.DefaultUserPageLimit, cms.MaxUserPageLimit, cms.MaxUserPageOffset)
}

func privatePageSchema(description, itemRef string, maxLimit, maxOffset int, example any) *huma.Schema {
	return closedObjectSchema(description, map[string]*huma.Schema{
		"items":       {Type: huma.TypeArray, Items: schemaRef(itemRef), MaxItems: intPointer(maxLimit)},
		"next_offset": {Type: huma.TypeInteger, Nullable: true, Minimum: floatPointer(0), Maximum: floatPointer(float64(maxOffset))},
	}, []string{"items", "next_offset"}, example)
}

func privatePageParameters(defaultLimit, maxLimit, maxOffset int) []*huma.Param {
	return []*huma.Param{
		{Name: "limit", In: "query", Description: fmt.Sprintf("Single unsigned decimal page size from 1 to %d; default %d. Unknown and repeated parameters are rejected.", maxLimit, defaultLimit), Schema: &huma.Schema{Type: huma.TypeInteger, Minimum: floatPointer(1), Maximum: floatPointer(float64(maxLimit)), Default: defaultLimit, Examples: []any{defaultLimit}}},
		{Name: "offset", In: "query", Description: fmt.Sprintf("Single unsigned decimal offset from 0 to %d; default 0. Use next_offset for the following page.", maxOffset), Schema: &huma.Schema{Type: huma.TypeInteger, Minimum: floatPointer(0), Maximum: floatPointer(float64(maxOffset)), Default: 0, Examples: []any{0}}},
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
