package content

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	scopeShared      = "shared"
	scopeLocalized   = "localized"
	visibilityPublic = "public"
	kindString       = "string"
	kindText         = "text"
	kindMarkdown     = "markdown"
	kindBoolean      = "boolean"
	kindInteger      = "integer"
	kindDecimal      = "decimal"
	kindDatetime     = "datetime"
	kindEnum         = "enum"
	kindRelation     = "relation"
)

type fieldConfig struct {
	MinLength *int     `json:"min_length,omitempty"`
	MaxLength *int     `json:"max_length,omitempty"`
	Min       *string  `json:"min,omitempty"`
	Max       *string  `json:"max,omitempty"`
	Codes     []string `json:"codes,omitempty"`
	Slug      bool     `json:"slug,omitempty"`
}

var integerPattern = regexp.MustCompile(`^(0|-[1-9][0-9]*|[1-9][0-9]*)$`)
var decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$`)

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return problem(Validation, invalidValueCode)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return problem(Validation, invalidValueCode)
	}
	return nil
}
func configFor(field Definition) (fieldConfig, error) {
	var config fieldConfig
	if len(field.Config) == 0 {
		field.Config = json.RawMessage(`{}`)
	}
	err := decodeStrict(field.Config, &config)
	return config, err
}

// ValidateType validates the same semantic input for initial seeds and Registry commands.
func ValidateType(input TypeInput) error {
	if !typeKeyPattern.MatchString(input.Key) || rendererTypeKey(input.RouteNamespace) != input.Key || len(input.Fields) == 0 || len(input.Fields) > 64 {
		return problem(Validation, "invalid_schema")
	}
	seen := map[string]bool{}
	fields := map[string]Definition{}
	for _, field := range input.Fields {
		if seen[field.Key] || !typeKeyPattern.MatchString(field.Key) {
			return problem(Validation, invalidFieldCode)
		}
		seen[field.Key] = true
		if err := validateDefinition(field); err != nil {
			return err
		}
		fields[field.Key] = field
	}
	return validateRendererFields(fields)
}
func validateRendererFields(fields map[string]Definition) error {
	for _, key := range []string{slugFieldKey, titleFieldKey} {
		field, exists := fields[key]
		if !exists || field.Kind != kindString || field.Scope != scopeLocalized || field.Visibility != visibilityPublic || !field.Required {
			return problem(Validation, rendererDependencyCode)
		}
		config, err := configFor(field)
		if err != nil {
			return err
		}
		if key == slugFieldKey && !config.Slug {
			return problem(Validation, rendererDependencyCode)
		}
		if key == titleFieldKey && (config.MinLength == nil || *config.MinLength < 1) {
			return problem(Validation, rendererDependencyCode)
		}
	}
	return nil
}

const rendererDependencyCode = "renderer_dependency"

func validateDefinition(field Definition) error {
	if field.Scope != scopeShared && field.Scope != scopeLocalized {
		return problem(Validation, "invalid_scope")
	}
	if field.Visibility != visibilityPublic && field.Visibility != visibilityPrivate {
		return problem(Validation, "invalid_visibility")
	}
	config, err := configFor(field)
	if err != nil {
		return err
	}
	if err := validateConfig(field, config); err != nil {
		return err
	}
	return validateLabels(field.DisplayLabels)
}
func validateConfig(field Definition, config fieldConfig) error {
	if err := validateOptionKeys(field); err != nil {
		return err
	}
	if field.Kind == kindRelation {
		if field.Required || field.TargetTypeID == "" {
			return problem(Validation, "invalid_relation")
		}
	} else if field.TargetTypeID != "" {
		return problem(Validation, "invalid_target")
	}
	if err := validateLengths(config); err != nil {
		return err
	}
	return validateBounds(field, config)
}
func validateOptionKeys(field Definition) error {
	allowed := map[string]bool{}
	switch field.Kind {
	case kindString, kindText, kindMarkdown:
		allowed = map[string]bool{"min_length": true, "max_length": true, slugFieldKey: true}
	case kindInteger, kindDecimal:
		allowed = map[string]bool{"min": true, "max": true}
	case kindEnum:
		allowed = map[string]bool{"codes": true}
	case kindBoolean, kindDatetime, kindRelation:
	default:
		return problem(Validation, "invalid_kind")
	}
	raw := defaultObject(field.Config)
	if len(raw) > 8192 {
		return problem(Validation, invalidOptionsCode)
	}
	var options map[string]json.RawMessage
	if err := decodeStrict(raw, &options); err != nil || options == nil {
		return problem(Validation, invalidOptionsCode)
	}
	for key := range options {
		if !allowed[key] {
			return problem(Validation, invalidOptionsCode)
		}
	}
	return nil
}
func validateLengths(config fieldConfig) error {
	if config.MinLength != nil && (*config.MinLength < 0 || *config.MinLength > 65536) {
		return problem(Validation, invalidLengthCode)
	}
	if config.MaxLength != nil && (*config.MaxLength < 1 || *config.MaxLength > 65536) {
		return problem(Validation, invalidLengthCode)
	}
	if config.MinLength != nil && config.MaxLength != nil && *config.MinLength > *config.MaxLength {
		return problem(Validation, invalidRangeCode)
	}
	return nil
}
func validateBounds(field Definition, config fieldConfig) error {
	for _, bound := range []*string{config.Min, config.Max} {
		if bound != nil {
			if err := validateNumber(field.Kind, *bound); err != nil {
				return err
			}
		}
	}
	if config.Min != nil && config.Max != nil {
		a, _ := new(big.Rat).SetString(*config.Min)
		b, _ := new(big.Rat).SetString(*config.Max)
		if a.Cmp(b) > 0 {
			return problem(Validation, invalidRangeCode)
		}
	}
	if field.Kind == kindEnum {
		return validateEnumCodes(config.Codes)
	}
	return nil
}
func validateEnumCodes(codes []string) error {
	if len(codes) == 0 || len(codes) > 128 {
		return problem(Validation, invalidEnumCode)
	}
	seen := map[string]bool{}
	for _, code := range codes {
		if !typeKeyPattern.MatchString(code) || seen[code] {
			return problem(Validation, invalidEnumCode)
		}
		seen[code] = true
	}
	return nil
}
func validateLabels(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > 8192 {
		return problem(Validation, "labels_limit")
	}
	var labels map[string]map[string]string
	if err := decodeStrict(raw, &labels); err != nil {
		return err
	}
	if labels == nil {
		return problem(Validation, invalidLabelsCode)
	}
	for locale, values := range labels {
		if err := validateLabelEntry(locale, values); err != nil {
			return err
		}
	}
	return nil
}
func validateNumber(kind, value string) error {
	if kind == kindInteger {
		if !integerPattern.MatchString(value) {
			return problem(Validation, "invalid_integer")
		}
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return problem(Validation, "invalid_integer")
		}
		return nil
	}
	if !decimalPattern.MatchString(value) || value == "-0" {
		return problem(Validation, invalidDecimalCode)
	}
	digits := strings.TrimPrefix(value, "-")
	parts := strings.Split(digits, ".")
	precision := len(parts[0])
	if len(parts) == 2 {
		precision += len(parts[1])
		if len(parts[1]) > 6 {
			return problem(Validation, invalidDecimalCode)
		}
	}
	if precision > 18 {
		return problem(Validation, invalidDecimalCode)
	}
	return nil
}
func validateValue(field Definition, tagged FieldValue) (json.RawMessage, error) {
	if tagged.Kind != field.Kind || field.Kind == kindRelation || len(tagged.Value) == 0 || bytes.Equal(bytes.TrimSpace(tagged.Value), []byte("null")) {
		return nil, problem(Validation, invalidValueCode)
	}
	config, err := configFor(field)
	if err != nil {
		return nil, err
	}
	if field.Kind == kindBoolean {
		var value bool
		if err := decodeStrict(tagged.Value, &value); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(value)
		return raw, nil
	}
	var value string
	if err := decodeStrict(tagged.Value, &value); err != nil {
		return nil, err
	}
	if len(value) > 65536 || !utf8.ValidString(value) {
		return nil, problem(Validation, "value_limit")
	}
	if err := validateScalar(field.Kind, value, config); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(value)
	return raw, nil
}
func validateScalar(kind, value string, config fieldConfig) error {
	switch kind {
	case kindString, kindText, kindMarkdown:
		return validateText(value, config)
	case kindInteger, kindDecimal:
		if err := validateNumber(kind, value); err != nil {
			return err
		}
		return validateNumericRange(value, config)
	case kindDatetime:
		return validateDatetime(value)
	case kindEnum:
		for _, code := range config.Codes {
			if value == code {
				return nil
			}
		}
		return problem(Validation, invalidEnumCode)
	}
	return nil
}
func validateText(value string, config fieldConfig) error {
	if !plainText(value) {
		return problem(Validation, "unsafe_text")
	}
	length := utf8.RuneCountInString(value)
	if config.MinLength != nil && length < *config.MinLength || config.MaxLength != nil && length > *config.MaxLength {
		return problem(Validation, invalidLengthCode)
	}
	if config.Slug && (!slugPattern.MatchString(value) || len(value) > 200) {
		return problem(Validation, invalidSlugCode)
	}
	return nil
}
func validateDatetime(value string) error {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !strings.HasSuffix(value, "Z") || parsed.Nanosecond()%1000 != 0 || parsed.Format(time.RFC3339Nano) != value {
		return problem(Validation, "invalid_datetime")
	}
	return nil
}
func validateNumericRange(value string, config fieldConfig) error {
	number, _ := new(big.Rat).SetString(value)
	if config.Min != nil {
		bound, _ := new(big.Rat).SetString(*config.Min)
		if number.Cmp(bound) < 0 {
			return problem(Validation, invalidRangeCode)
		}
	}
	if config.Max != nil {
		bound, _ := new(big.Rat).SetString(*config.Max)
		if number.Cmp(bound) > 0 {
			return problem(Validation, invalidRangeCode)
		}
	}
	return nil
}
func validateValues(schema TypeSchema, scope string, values ScalarValues, required bool) error {
	fields := map[string]Definition{}
	for _, field := range schema.Fields {
		fields[field.Key] = field
		if required && field.Scope == scope && field.Required {
			if _, ok := values[field.Key]; !ok {
				return problem(Validation, "required_field")
			}
		}
	}
	for key, value := range values {
		field, ok := fields[key]
		if !ok || field.Scope != scope {
			return problem(Validation, invalidFieldCode)
		}
		if _, err := validateValue(field, FieldValue{field.Kind, value}); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(values)
	if err != nil || len(raw) > 256*1024 {
		return problem(Validation, "values_limit")
	}
	return nil
}

const invalidLabelsCode = "invalid_labels"

func validateLabelEntry(locale string, values map[string]string) error {
	if values == nil {
		return problem(Validation, invalidLabelsCode)
	}
	if _, err := normalizeLanguage(locale); err != nil {
		return err
	}
	for key, value := range values {
		if (key != "label" && key != "help") || !plainText(value) || len(value) > 4096 {
			return problem(Validation, invalidLabelsCode)
		}
	}
	return nil
}
