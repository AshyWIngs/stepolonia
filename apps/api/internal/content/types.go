// Package content implements the installation-local Collection Registry and Engine.
package content

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/text/language"
	"stepolonia/apps/api/internal/identity"
)

const (
	scopeShared    = "shared"
	scopeLocalized = "localized"
)

const (
	visibilityPublic  = "public"
	visibilityPrivate = "private"
)

const (
	kindString   = "string"
	kindText     = "text"
	kindMarkdown = "markdown"
	kindBoolean  = "boolean"
	kindInteger  = "integer"
	kindDecimal  = "decimal"
	kindDatetime = "datetime"
	kindEnum     = "enum"
	kindRelation = "relation"
)

var typeKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func normalizeLanguage(code string) (string, error) {
	if code == "" || len(code) > 64 || strings.ContainsAny(code, "_@ \t\r\n") {
		return "", problem(Validation, invalidLanguageCode)
	}
	tag, err := language.Parse(code)
	if err != nil || tag == language.Und {
		return "", problem(Validation, invalidLanguageCode)
	}
	return strings.ToLower(tag.String()), nil
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Actor is an opaque server authentication context. No public caller can mint authority.
type Actor struct{}
type FieldValue struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}
type ScalarValues map[string]json.RawMessage
type Definition struct {
	ID            string
	Key           string
	Kind          string
	Scope         string
	Visibility    string
	Required      bool
	TargetTypeID  string
	Config        json.RawMessage
	DisplayLabels json.RawMessage
}
type TypeSchema struct {
	TypeID         string
	Key            string
	RouteNamespace string
	Version        int64
	SchemaNo       int64
	Fields         []Definition
}
type TypeInput struct {
	Key            string
	RouteNamespace string
	Fields         []Definition
}
type Relation struct {
	FieldID      string
	Locale       string
	TargetTypeID string
	TargetItemID string
}
type ReadyTuple struct {
	LocalSeq         int64
	SharedSeq        int64
	SchemaNo         int64
	LocaleGeneration int64
}
type LocaleDraft struct {
	Values   ScalarValues
	LocalSeq int64
	Ready    *ReadyTuple
}
type DraftState struct {
	ItemID         string
	TypeID         string
	SchemaNo       int64
	Version        int64
	Shared         ScalarValues
	SharedSeq      int64
	SharedReadySeq sql.NullInt64
	Locales        map[string]LocaleDraft
	Relations      []Relation
	PublicationID  string
}
type DraftPatch struct {
	SchemaNo         int64
	Shared           map[string]FieldValue
	UnsetShared      []string
	Locales          map[string]ValuePatch
	Relations        []Relation
	ReplaceRelations bool
}
type ValuePatch struct {
	Values map[string]FieldValue
	Unset  []string
}
type authorizeFunc func(context.Context, *sql.Tx, Actor, string, string, string, []string) error

// Service has no enabled authorization adapter until authenticated administration sessions exist.
type Service struct {
	db        *sql.DB
	authorize authorizeFunc
	scope     scopeFunc
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, authorize: func(context.Context, *sql.Tx, Actor, string, string, string, []string) error {
		return problem(Forbidden, string(Forbidden))
	}}
}

type Registry = Service
type ContentService = Service

func (s *Service) withTx(ctx context.Context, actor Actor, op, typeID, itemID string, fields []string, fn func(context.Context, *sql.Tx) error) error {
	return databaseError(identity.WithWriteMutex(ctx, s.db, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.authorize(ctx, tx, actor, op, typeID, itemID, fields); err != nil {
			return err
		}
		return fn(ctx, tx)
	}))
}
func fieldKeys(schema TypeSchema) []string {
	keys := make([]string, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		keys = append(keys, f.Key)
	}
	return keys
}

func plainText(s string) bool { return !strings.ContainsAny(s, "<>\x00") }
