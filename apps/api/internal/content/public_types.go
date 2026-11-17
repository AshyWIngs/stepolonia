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
	Kind  string          `json:"kind" doc:"Registry scalar kind, selecting the JSON value codec."`
	Value json.RawMessage `json:"value" doc:"Scalar value. Boolean uses JSON boolean. Other kinds use strings, including exact integers, decimals and RFC 3339 timestamps."`
}

func (PublicFieldValue) Schema(huma.Registry) *huma.Schema {
	variants := make([]*huma.Schema, 0, 8)
	for _, kind := range []string{kindString, kindText, kindMarkdown, kindEnum, kindInteger, kindDecimal, kindDatetime, kindBoolean} {
		valueType := kindString
		if kind == kindBoolean {
			valueType = kindBoolean
		}
		variants = append(variants, &huma.Schema{Description: "A public " + kind + " value encoded according to the pinned Registry field definition.", Type: "object", Required: []string{fieldKindDiscriminator, fieldValueProperty}, Properties: map[string]*huma.Schema{fieldKindDiscriminator: {Type: kindString, Description: "Registry scalar kind selecting this variant.", Enum: []any{kind}, Examples: []any{kind}}, fieldValueProperty: {Type: valueType, Description: "Boolean uses a JSON boolean. All other kinds use strings. Integer and decimal preserve exact precision. Datetime uses RFC 3339 with an explicit UTC offset."}}, AdditionalProperties: false})
	}
	return &huma.Schema{Description: "One public scalar value tagged by Registry kind. Each variant has exactly kind and value. Field-specific constraints come from the pinned Registry schema. Markdown remains source text and must be rendered safely.", OneOf: variants, Discriminator: &huma.Discriminator{PropertyName: fieldKindDiscriminator}}
}

type PublicTranslation struct {
	Language     string `json:"language" doc:"Normalized BCP 47 content language. Only published translations in active languages are exposed." example:"pl"`
	Slug         string `json:"slug" doc:"Current canonical slug in this language."`
	CanonicalURL string `json:"canonical_url" doc:"Relative public website path, including language and route namespace."`
}
type PublicTarget struct {
	ItemID       string `json:"item_id" format:"uuid" doc:"Stable item UUID shared by all translations."`
	CanonicalURL string `json:"canonical_url" doc:"Relative public website path, including language and route namespace."`
}
type PublicRelation struct {
	FieldKey string         `json:"field_key" doc:"Registry relation field key from the pinned schema." example:"related"`
	Targets  []PublicTarget `json:"targets" minItems:"1" maxItems:"1" nullable:"false" doc:"Published target in the source language. Exactly one target is exposed for each returned relation."`
}
type PublicItem struct {
	ItemID        string                      `json:"item_id" format:"uuid" doc:"Stable item UUID shared by all translations."`
	TypeKey       string                      `json:"type_key" doc:"Public collection key from the Registry." example:"article"`
	SchemaNo      string                      `json:"schema_no" pattern:"^[1-9][0-9]*$" doc:"Pinned Registry schema version as a positive base-10 string, preserving int64 precision." example:"1"`
	PublicationID string                      `json:"publication_id" format:"uuid" doc:"UUID of the immutable publication supplying this response."`
	Language      string                      `json:"language" doc:"Normalized BCP 47 content language. Only published translations in active languages are exposed." example:"pl"`
	Slug          string                      `json:"slug" doc:"Current canonical slug in this language."`
	CanonicalURL  string                      `json:"canonical_url" doc:"Relative public website path, including language and route namespace."`
	Translations  []PublicTranslation         `json:"translations" nullable:"false" doc:"Available published translations, including the current language, ordered by language code. Empty arrays are serialized as []."`
	Shared        map[string]PublicFieldValue `json:"shared" doc:"Public scalar values shared across languages, keyed by pinned Registry field key. Absent optional values have no entry. An empty map is {}."`
	Localized     map[string]PublicFieldValue `json:"localized" doc:"Public scalar values for this exact language, keyed by pinned Registry field key. No language fallback. Absent optional values have no entry."`
	Relations     []PublicRelation            `json:"relations" nullable:"false" doc:"Public relations with a published target in this language, ordered by field key. Unavailable targets are omitted. Empty arrays are serialized as []."`
}
type PublicPage struct {
	Items      []PublicItem `json:"items" nullable:"false" doc:"Published items ordered by publication time descending, then item UUID descending. An empty collection is []."`
	NextOffset *int         `json:"next_offset" doc:"Offset for the next page, or null when no further page is available within the maximum offset of 10000."`
}
type Resolution struct {
	Item     PublicItem
	Redirect bool
}

// TransformSchema documents the public PublicTranslation contract without changing validation.
func (PublicTranslation) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "A published translation link for the same item. Only currently public languages and publications are returned."
	return schema
}

// TransformSchema documents the public PublicTarget contract without changing validation.
func (PublicTarget) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "A public relation target resolved in the source item language. The URL points to the current canonical website route."
	return schema
}

// TransformSchema documents the public PublicRelation contract without changing validation.
func (PublicRelation) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "A visible Registry relation with its single published same-language target. Relations with unavailable targets are omitted."
	return schema
}

// TransformSchema documents the public PublicItem contract without changing validation.
func (PublicItem) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "A published content snapshot for one exact language and pinned Registry schema. Only public fields and publicly resolvable relations are returned. Optional absent scalar values are omitted from maps, and values never use JSON null."
	return schema
}

// TransformSchema documents the public PublicPage contract without changing validation.
func (PublicPage) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Description = "An offset-paginated collection of published content. next_offset is null at the end or when the next offset would exceed 10000."
	return schema
}
