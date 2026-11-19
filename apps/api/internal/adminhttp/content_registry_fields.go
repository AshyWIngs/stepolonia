package adminhttp

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"stepolonia/apps/api/internal/content"
	"stepolonia/apps/api/internal/identity"
)

const (
	expectedSchemaField = "expected_schema_no"
	fieldLabelsField    = "display_labels"
	addFieldsField      = "add"
	editLabelsField     = "labels"
	contentKeyPattern   = `^[a-z][a-z0-9_]{0,63}$`
)

type registryFieldChangesInput struct {
	ExpectedSchema int64                 `json:"expected_schema_no,string,omitempty"`
	Add            []registryField       `json:"add,omitempty"`
	Labels         []registryLabelChange `json:"labels,omitempty"`
	Initialize     bool                  `json:"initialize_new_field_access,omitempty"`
}

type registryLabelChange struct {
	Key             string          `json:"key"`
	ExpectedVersion string          `json:"expected_version"`
	DisplayLabels   json.RawMessage `json:"display_labels"`
}

func runRegistryFieldChanges(ctx context.Context, req identity.AuthRequest, key string, raw []byte, service RegistryEditor) (content.RegistryWriteResult, error) {
	var input registryFieldChangesInput
	if !decodeRegistryFieldChanges(raw, &input) {
		return content.RegistryWriteResult{}, identity.ErrValidation
	}
	labels := make([]content.FieldLabelChange, len(input.Labels))
	for i, label := range input.Labels {
		labels[i] = content.FieldLabelChange{Key: label.Key, ExpectedVersion: label.ExpectedVersion, DisplayLabels: label.DisplayLabels}
	}
	return service.ChangeCollectionFields(ctx, req, content.CollectionFieldChanges{
		TypeKey: key, ExpectedSchema: input.ExpectedSchema, Add: registryDefinitions(input.Add), Labels: labels, Initialize: input.Initialize,
	})
}

func decodeRegistryFieldChanges(raw []byte, input *registryFieldChangesInput) bool {
	value, ok := decodeUniqueJSON(raw)
	object, objectOK := value.(map[string]any)
	schema := registryWriteContracts[registryFieldChangesOperation]
	if !ok || !objectOK || !utf8.Valid(raw) || !onlyRegistryKeys(object, schema) {
		return false
	}
	if !closedRegistryItems(object, schema, addFieldsField) || !closedRegistryItems(object, schema, editLabelsField) {
		return false
	}
	var result huma.ValidateResult
	huma.Validate(nil, schema, huma.NewPathBuffer(nil, 0), huma.ModeWriteToServer, value, &result)
	if len(result.Errors) != 0 || json.Unmarshal(raw, input) != nil {
		return false
	}
	if len(input.Add) > 0 {
		return input.ExpectedSchema > 0
	}
	return len(input.Labels) > 0 && !input.Initialize
}

func closedRegistryItems(object map[string]any, schema *huma.Schema, field string) bool {
	items, ok := object[field].([]any)
	if !ok {
		return object[field] == nil
	}
	for _, item := range items {
		fields, ok := item.(map[string]any)
		if !ok || !onlyRegistryKeys(fields, schema.Properties[field].Items) {
			return false
		}
	}
	return true
}

func registryFieldChangesSchema() *huma.Schema {
	const fieldKey = "key"
	label := closedObjectSchema("Replace display labels for one field.", map[string]*huma.Schema{
		fieldKey:             {Type: huma.TypeString, Pattern: contentKeyPattern},
		expectedVersionField: registryScopeVersionSchema("Current version of this field's display labels."),
		fieldLabelsField:     {Type: huma.TypeObject, Description: "Locale-keyed plain-text labels and help. An empty object removes overrides."},
	}, []string{fieldKey, expectedVersionField, fieldLabelsField}, nil)
	schema := closedObjectSchema("Add optional fields or replace one or more field label sets.", map[string]*huma.Schema{
		expectedSchemaField:           contentVersionSchema("Current schema number, required when adding fields.", "1"),
		addFieldsField:                registryFieldsSchema(),
		editLabelsField:               {Type: huma.TypeArray, Items: label, MinItems: new(1), MaxItems: new(64)},
		"initialize_new_field_access": {Type: huma.TypeBoolean, Description: "Request exact grants for newly added optional fields."},
	}, nil, registryFieldChangesInput{
		Labels: []registryLabelChange{{
			Key: "headline", ExpectedVersion: content.FieldLabelVersion(nil), DisplayLabels: json.RawMessage(`{"pl":{"label":"Tytuł"}}`),
		}},
	})
	add := &huma.Schema{Type: huma.TypeObject, Required: []string{addFieldsField, expectedSchemaField},
		Properties: map[string]*huma.Schema{addFieldsField: schema.Properties[addFieldsField], expectedSchemaField: schema.Properties[expectedSchemaField]}}
	labels := &huma.Schema{Type: huma.TypeObject, Required: []string{editLabelsField},
		Properties: map[string]*huma.Schema{editLabelsField: schema.Properties[editLabelsField]}}
	schema.AnyOf = []*huma.Schema{add, labels}
	schema.MinProperties = new(1)
	schema.DependentRequired = map[string][]string{addFieldsField: {expectedSchemaField}}
	schema.PrecomputeMessages()
	return schema
}
