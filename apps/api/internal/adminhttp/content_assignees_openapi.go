package adminhttp

import (
	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/identity"
)

const (
	contentAssigneesTag    = "content-assignments"
	contentAssigneesSchema = "ContentAssignees"
	itemIDField            = "item_id"
)

type contentAssigneesResponse struct {
	ItemID      string   `json:"item_id"`
	Version     int64    `json:"version,string"`
	AssigneeIDs []string `json:"assignee_ids"`
}

func contentTypeParameter(description string) *huma.Param {
	typeKey := stringSchema("Stable Registry collection key.", "article")
	typeKey.Pattern = `^[a-z][a-z0-9_]{0,63}$`
	return &huma.Param{Name: "type_key", In: openAPIPathParameter, Required: true, Description: description, Schema: typeKey}
}

func contentItemParameters() []*huma.Param {
	itemID := uuidSchema(exampleUUID)
	itemID.Description = "Stable content item UUID."
	return []*huma.Param{
		contentTypeParameter("Collection key that must match the item's actual type."),
		{Name: itemIDField, In: openAPIPathParameter, Required: true, Description: "Item UUID within the named collection.", Schema: itemID},
	}
}

func assigneeIDsSchema(description string) *huma.Schema {
	item := uuidSchema(exampleUUID)
	item.Description = "Stable account UUID."
	return &huma.Schema{Type: huma.TypeArray, Items: item, MaxItems: intPointer(identity.MaxContentAssignees), UniqueItems: true, Description: description}
}

func contentVersionSchema(description, example string) *huma.Schema {
	schema := stringSchema(description+" Its canonical decimal representation ranges from 1 through 9223372036854775807 to preserve int64 precision.", example)
	schema.Pattern = `^[1-9][0-9]{0,18}$`
	return schema
}

func contentAssigneesRequest() (*huma.Schema, any) {
	version := contentVersionSchema("Current item version checked under the shared security mutex before replacement.", "2")
	ids := assigneeIDsSchema("Complete assignee set. All supplied accounts must be active. Repeated UUIDs are rejected regardless of case. An empty array clears all assignments.")
	example := replaceContentAssigneesInput{ExpectedVersion: 2, AssigneeIDs: []string{exampleUUID}}
	schema := closedObjectSchema("Versioned replacement of the complete assignee set. Both properties are required. A null array is invalid.",
		map[string]*huma.Schema{expectedVersionField: version, assigneeIDsField: ids}, []string{expectedVersionField, assigneeIDsField}, example)
	schema.Examples = append(schema.Examples, replaceContentAssigneesInput{ExpectedVersion: 3, AssigneeIDs: []string{}})
	return schema, example
}

func contentAssigneesResultSchema() *huma.Schema {
	item := uuidSchema(exampleUUID)
	item.Description = "Stable UUID of the updated content item."
	ids := assigneeIDsSchema("Committed assignee UUIDs in canonical order. An empty set is represented by [].")
	version := contentVersionSchema("Current item version after the replacement; use it for the next mutation.", "3")
	return closedObjectSchema("Committed assignment set and version of the updated content item.",
		map[string]*huma.Schema{itemIDField: item, "version": version, assigneeIDsField: ids}, []string{itemIDField, "version", assigneeIDsField},
		contentAssigneesResponse{ItemID: exampleUUID, Version: 3, AssigneeIDs: []string{exampleUUID}})
}

func contentAssigneesErrorCodes() map[int][]string {
	return map[int][]string{
		400: {validationCode}, 401: {authenticationCode}, 403: {originCode, csrfCode, permissionCode, stepUpRequiredCode},
		404: {notFoundCode}, 405: {methodNotAllowedCode}, 409: {versionConflictCode, invalidTransitionCode},
		413: {bodyTooLargeCode}, 429: {rateLimitedCode}, 503: {securityUnavailableCode},
	}
}
