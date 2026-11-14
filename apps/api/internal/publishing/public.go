package publishing

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
)

// PublicReader получает отдельный runtime pool; publisher pool для публичного чтения запрещён.
type PublicReader struct{ db *sql.DB }

func NewPublicReader(runtimeDB *sql.DB) *PublicReader { return &PublicReader{db: runtimeDB} }

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (r *PublicReader) begin(ctx context.Context) (*sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, databaseError(err)
	}
	var allowed bool
	// Проверка и view SELECT выполняются на одном соединении. Пониженный ROLE
	// owner-session не заменяет непривилегированное подключение будущего HTTP API.
	err = tx.QueryRowContext(ctx, `SELECT current_user=session_user
 AND NOT (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls)
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
 AND NOT pg_catalog.has_schema_privilege(r.oid,'public','CREATE')
 AND NOT pg_catalog.has_database_privilege(r.oid,current_database(),'CREATE,TEMP')
 AND NOT EXISTS (SELECT 1 FROM (VALUES('public.languages'),('public.articles'),
 ('public.article_draft_translations'),('public.article_revisions'),
 ('public.article_revision_translations'),('public.public_routes')) AS private(name)
 WHERE pg_catalog.has_table_privilege(r.oid,private.name,'SELECT')
 OR pg_catalog.has_any_column_privilege(r.oid,private.name,'SELECT'))
 FROM pg_catalog.pg_roles r WHERE rolname=current_user`).Scan(&allowed)
	if err != nil || !allowed {
		rollback(tx)
		if err != nil {
			return nil, databaseError(err)
		}
		return nil, problem(Forbidden, "public_connection_required")
	}
	return tx, nil
}

func (r *PublicReader) ListLanguages(ctx context.Context) ([]Language, error) {
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	rows, err := tx.QueryContext(ctx, "SELECT code,name,native_name,direction FROM public.public_languages ORDER BY code COLLATE \"C\"")
	if err != nil {
		return nil, databaseError(err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]Language, 0)
	for rows.Next() {
		var value Language
		if err = rows.Scan(&value.Code, &value.Name, &value.NativeName, &value.Direction); err != nil {
			return nil, databaseError(err)
		}
		result = append(result, value)
	}
	if err = rows.Err(); err != nil {
		return nil, databaseError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, databaseError(err)
	}
	return result, nil
}

func (r *PublicReader) ListArticles(ctx context.Context, languageCode string, page Page) ([]PublicArticle, error) {
	code, err := normalizeLanguage(languageCode)
	if err != nil {
		return nil, err
	}
	if err = validatePage(page); err != nil {
		return nil, err
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if err = requirePublicLanguage(ctx, tx, code); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT article_id,revision_id,language_code,title,slug,body_markdown
 FROM public.public_article_translations WHERE language_code=$1 ORDER BY published_at DESC,article_id DESC LIMIT $2 OFFSET $3`, code, page.Limit, page.Offset)
	if err != nil {
		return nil, databaseError(err)
	}
	articles, err := scanPublicArticles(rows)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, databaseError(err)
	}
	return articles, nil
}

func requirePublicLanguage(ctx context.Context, tx *sql.Tx, code string) error {
	var enabled bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM public.public_languages WHERE code=$1)", code).Scan(&enabled)
	if err != nil {
		return databaseError(err)
	}
	if !enabled {
		return problem(NotFound, "language_not_found")
	}
	return nil
}

func scanPublicArticles(rows *sql.Rows) ([]PublicArticle, error) {
	defer func() { _ = rows.Close() }()
	result := make([]PublicArticle, 0)
	for rows.Next() {
		var article PublicArticle
		if err := rows.Scan(&article.ArticleID, &article.RevisionID, &article.LanguageCode, &article.Title, &article.Slug, &article.BodyMarkdown); err != nil {
			return nil, databaseError(err)
		}
		result = append(result, article)
	}
	return result, databaseError(rows.Err())
}

func (r *PublicReader) ResolveArticle(ctx context.Context, languageCode, slug string) (Resolution, error) {
	code, err := normalizeLanguage(languageCode)
	if err != nil {
		return Resolution{}, err
	}
	if len(slug) > 200 || !slugPattern.MatchString(slug) {
		return Resolution{}, problem(Validation, "invalid_slug")
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return Resolution{}, err
	}
	defer rollback(tx)
	var result Resolution
	article := &result.Article
	err = tx.QueryRowContext(ctx, `SELECT t.article_id,t.revision_id,t.language_code,t.title,t.slug,t.body_markdown
 FROM public.public_article_routes r JOIN public.public_article_translations t
 ON t.article_id=r.article_id AND t.revision_id=r.revision_id AND t.language_code=r.language_code
 WHERE r.language_code=$1 AND r.slug=$2`, code, slug).Scan(&article.ArticleID, &article.RevisionID, &article.LanguageCode, &article.Title, &article.Slug, &article.BodyMarkdown)
	if errors.Is(err, sql.ErrNoRows) {
		return Resolution{}, problem(NotFound, articleNotFoundCode)
	}
	if err != nil {
		return Resolution{}, databaseError(err)
	}
	result.Redirect = article.Slug != slug
	if err = tx.Commit(); err != nil {
		return Resolution{}, databaseError(err)
	}
	return result, nil
}
