package publishing

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Service) GetEditorialList(ctx context.Context, page Page) ([]ArticleState, error) {
	if err := validatePage(page); err != nil {
		return nil, err
	}
	tx, err := s.begin(ctx, true)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	rows, err := tx.QueryContext(ctx, "SELECT id,version,published_revision_id FROM public.articles ORDER BY id LIMIT $1 OFFSET $2", page.Limit, page.Offset)
	if err != nil {
		return nil, databaseError(err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]ArticleState, 0)
	for rows.Next() {
		var state ArticleState
		if err = rows.Scan(&state.ID, &state.Version, &state.PublishedRevisionID); err != nil {
			return nil, databaseError(err)
		}
		result = append(result, state)
	}
	if err = rows.Err(); err != nil {
		return nil, databaseError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, databaseError(err)
	}
	return result, nil
}

func (s *Service) GetEditorialDetail(ctx context.Context, id string) (EditorialArticle, error) {
	if err := validateID(id); err != nil {
		return EditorialArticle{}, err
	}
	tx, err := s.begin(ctx, true)
	if err != nil {
		return EditorialArticle{}, err
	}
	defer rollback(tx)
	var article EditorialArticle
	err = tx.QueryRowContext(ctx, "SELECT id,version,published_revision_id FROM public.articles WHERE id=$1", id).Scan(&article.ID, &article.Version, &article.PublishedRevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return EditorialArticle{}, problem(NotFound, articleNotFoundCode)
	}
	if err != nil {
		return EditorialArticle{}, databaseError(err)
	}
	article.Drafts, err = draftTranslations(ctx, tx, id)
	if err != nil {
		return EditorialArticle{}, err
	}
	article.Published, err = currentTranslations(ctx, tx, article.PublishedRevisionID)
	if err != nil {
		return EditorialArticle{}, err
	}
	if err = tx.Commit(); err != nil {
		return EditorialArticle{}, databaseError(err)
	}
	return article, nil
}

func draftTranslations(ctx context.Context, tx *sql.Tx, id string) ([]DraftTranslation, error) {
	rows, err := tx.QueryContext(ctx, "SELECT language_code,title,slug,body_markdown,is_ready FROM public.article_draft_translations WHERE article_id=$1 ORDER BY language_code COLLATE \"C\"", id)
	if err != nil {
		return nil, databaseError(err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]DraftTranslation, 0)
	for rows.Next() {
		var value DraftTranslation
		if err = rows.Scan(&value.LanguageCode, &value.Title, &value.Slug, &value.BodyMarkdown, &value.IsReady); err != nil {
			return nil, databaseError(err)
		}
		result = append(result, value)
	}
	return result, databaseError(rows.Err())
}
