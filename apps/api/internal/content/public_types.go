package content

import (
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
)

const fieldKindDiscriminator = "kind"
const fieldValueProperty = "value"

type Page struct{ Limit, Offset int }
type Language struct{ Code, Name, NativeName, Direction string }

// PublicFieldValue preserves exact integer and decimal codecs and boolean JSON types.
type PublicFieldValue struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

func (PublicFieldValue) Schema(huma.Registry) *huma.Schema {
	variants := make([]*huma.Schema, 0, 8)
	for _, kind := range []string{kindString, kindText, kindMarkdown, kindEnum, kindInteger, kindDecimal, kindDatetime, kindBoolean} {
		valueType := kindString
		if kind == kindBoolean {
			valueType = kindBoolean
		}
		variants = append(variants, &huma.Schema{Type: "object", Required: []string{fieldKindDiscriminator, fieldValueProperty}, Properties: map[string]*huma.Schema{fieldKindDiscriminator: {Type: kindString, Enum: []any{kind}}, fieldValueProperty: {Type: valueType}}, AdditionalProperties: false})
	}
	return &huma.Schema{OneOf: variants, Discriminator: &huma.Discriminator{PropertyName: fieldKindDiscriminator}}
}

type PublicTranslation struct {
	Language     string `json:"language"`
	Slug         string `json:"slug"`
	CanonicalURL string `json:"canonical_url"`
}
type PublicTarget struct {
	ItemID       string `json:"item_id" format:"uuid"`
	CanonicalURL string `json:"canonical_url"`
}
type PublicRelation struct {
	FieldKey string         `json:"field_key"`
	Targets  []PublicTarget `json:"targets" minItems:"1" maxItems:"1" nullable:"false"`
}
type PublicItem struct {
	ItemID        string                      `json:"item_id" format:"uuid"`
	TypeKey       string                      `json:"type_key"`
	SchemaNo      string                      `json:"schema_no" pattern:"^[1-9][0-9]*$"`
	PublicationID string                      `json:"publication_id" format:"uuid"`
	Language      string                      `json:"language"`
	Slug          string                      `json:"slug"`
	CanonicalURL  string                      `json:"canonical_url"`
	Translations  []PublicTranslation         `json:"translations" nullable:"false"`
	Shared        map[string]PublicFieldValue `json:"shared"`
	Localized     map[string]PublicFieldValue `json:"localized"`
	Relations     []PublicRelation            `json:"relations" nullable:"false"`
}
type PublicPage struct {
	Items      []PublicItem `json:"items" nullable:"false"`
	NextOffset *int         `json:"next_offset"`
}
type Resolution struct {
	Item     PublicItem
	Redirect bool
}
