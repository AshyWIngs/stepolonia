package content

import (
	"context"
	"encoding/json"

	"stepolonia/apps/api/internal/identity"
)

type FieldLabelChange struct {
	Key             string
	ExpectedVersion string
	DisplayLabels   json.RawMessage
}

type CollectionFieldChanges struct {
	TypeKey        string
	ExpectedSchema int64
	Add            []Definition
	Labels         []FieldLabelChange
	Initialize     bool
}

func FieldLabelVersion(labels json.RawMessage) string {
	return collectionVersionToken(string(canonicalJSON(defaultObject(labels))))
}

func (e *DraftEditor) ChangeCollectionFields(ctx context.Context, req identity.AuthRequest, input CollectionFieldChanges) (RegistryWriteResult, error) {
	return e.reviseManagedCollection(ctx, req, input.TypeKey, input.Initialize, func(current *TypeSchema) ([]Definition, error) {
		return mergeCollectionFields(*current, input)
	})
}

func mergeCollectionFields(current TypeSchema, input CollectionFieldChanges) ([]Definition, error) {
	if len(input.Add)+len(input.Labels) == 0 || input.Initialize && len(input.Add) == 0 {
		return nil, problem(Validation, invalidFieldCode)
	}
	if len(input.Add) > 0 && current.SchemaNo != input.ExpectedSchema {
		return nil, problem(Conflict, versionConflictCode)
	}
	fields := append([]Definition(nil), current.Fields...)
	positions := make(map[string]int, len(fields))
	for i, field := range fields {
		positions[field.Key] = i
	}
	for _, field := range input.Add {
		if _, exists := positions[field.Key]; exists || field.ID != "" || field.Required {
			return nil, problem(Validation, invalidFieldCode)
		}
	}
	if err := applyFieldLabels(fields, positions, input.Labels); err != nil {
		return nil, err
	}
	return append(fields, input.Add...), nil
}

func applyFieldLabels(fields []Definition, positions map[string]int, labels []FieldLabelChange) error {
	seen := make(map[string]bool, len(labels))
	for _, label := range labels {
		position, exists := positions[label.Key]
		if !exists || seen[label.Key] {
			return problem(Validation, invalidFieldCode)
		}
		seen[label.Key] = true
		if FieldLabelVersion(fields[position].DisplayLabels) != label.ExpectedVersion {
			return problem(Conflict, versionConflictCode)
		}
		fields[position].DisplayLabels = label.DisplayLabels
	}
	return nil
}
