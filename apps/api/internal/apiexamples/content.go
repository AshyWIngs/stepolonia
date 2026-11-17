// Package apiexamples supplies synthetic public API examples and local demo data.
package apiexamples

import (
	"encoding/json"
	"fmt"

	"stepolonia/apps/api/internal/content"
)

// Article returns the published Article shown in the public API documentation.
func Article() content.PublicItem {
	return localizedItem(content.PublicItem{
		ItemID:  "11111111-1111-4111-8111-111111111111",
		TypeKey: "article", SchemaNo: "1",
		PublicationID: "22222222-2222-4222-8222-222222222222",
		Language:      "pl", Slug: "welcome-to-stepolonia",
		Shared: map[string]content.PublicFieldValue{}, Relations: []content.PublicRelation{},
	}, "articles", "Welcome to Stepolonia", "Community stories and **events**.")
}

// SecondArticle provides a second published item for offset pagination examples.
func SecondArticle() content.PublicItem {
	article := Article()
	return localizedItem(content.PublicItem{
		ItemID:  "88888888-8888-4888-8888-888888888888",
		TypeKey: article.TypeKey, SchemaNo: article.SchemaNo,
		PublicationID: "99999999-9999-4999-8999-999999999999",
		Language:      article.Language, Slug: "community-news",
		Shared: map[string]content.PublicFieldValue{}, Relations: []content.PublicRelation{},
	}, "articles", "Community news", "Read the latest community news.")
}

// Event returns the published Event with exact scalar codecs and an Article relation.
func Event() content.PublicItem {
	article := Article()
	return localizedItem(content.PublicItem{
		ItemID:  "44444444-4444-4444-8444-444444444444",
		TypeKey: "event", SchemaNo: article.SchemaNo,
		PublicationID: "55555555-5555-4555-8555-555555555555",
		Language:      article.Language, Slug: "community-meetup",
		Shared: map[string]content.PublicFieldValue{
			"featured": {Kind: "boolean", Value: json.RawMessage(`true`)},
			"price":    {Kind: "decimal", Value: json.RawMessage(`"12.5"`)},
		},
		Relations: []content.PublicRelation{
			{FieldKey: "related", Targets: []content.PublicTarget{
				{ItemID: article.ItemID, CanonicalURL: article.CanonicalURL},
			}},
		},
	}, "events", "Community meetup", "Meet the **Stepolonia** community.")
}

// EventSchema defines the pinned Registry schema used by the synthetic Event.
// articleTypeID is the installation's initial Article type identity.
func EventSchema(articleTypeID string) content.TypeSchema {
	// Visibility and scopes apply only to this fixed demonstration schema.
	const (
		publicVisibility = "public"
		sharedScope      = "shared"
		localizedScope   = "localized"
		shortTextKind    = "string"
	)
	event := Event()
	schema := content.TypeSchema{
		TypeID: "66666666-6666-4666-8666-666666666666",
		Key:    event.TypeKey, Version: 1, SchemaNo: 1, RouteNamespace: "events",
		Fields: []content.Definition{
			{Key: "title", Kind: shortTextKind, Scope: localizedScope, Visibility: publicVisibility, Required: true, Config: json.RawMessage(`{"min_length":1,"max_length":500}`)},
			{Key: "slug", Kind: shortTextKind, Scope: localizedScope, Visibility: publicVisibility, Required: true, Config: json.RawMessage(`{"slug":true,"max_length":200}`)},
			{Key: "body", Kind: "markdown", Scope: localizedScope, Visibility: publicVisibility, Required: true, Config: json.RawMessage(`{"min_length":1}`)},
			{Key: "featured", Kind: "boolean", Scope: sharedScope, Visibility: publicVisibility, Config: json.RawMessage(`{}`)},
			{Key: "price", Kind: "decimal", Scope: sharedScope, Visibility: publicVisibility, Config: json.RawMessage(`{}`)},
			{Key: "related", Kind: "relation", Scope: sharedScope, Visibility: publicVisibility, TargetTypeID: articleTypeID, Config: json.RawMessage(`{}`)},
		},
	}
	for i := range schema.Fields {
		schema.Fields[i].ID = fmt.Sprintf("77777777-7777-4777-8777-%012d", i+1)
	}
	return schema
}

func localizedItem(item content.PublicItem, namespace, title, body string) content.PublicItem {
	item.CanonicalURL = "/" + item.Language + "/" + namespace + "/" + item.Slug
	item.Translations = []content.PublicTranslation{
		{Language: item.Language, Slug: item.Slug, CanonicalURL: item.CanonicalURL},
	}
	item.Localized = map[string]content.PublicFieldValue{
		"title": stringValue(title),
		"slug":  stringValue(item.Slug),
		"body":  {Kind: "markdown", Value: quoted(body)},
	}
	return item
}

func stringValue(value string) content.PublicFieldValue {
	return content.PublicFieldValue{Kind: "string", Value: quoted(value)}
}

func quoted(value string) json.RawMessage {
	// JSON string encoding cannot fail for a Go string.
	raw, _ := json.Marshal(value)
	return raw
}
