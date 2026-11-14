package publishing

import (
	"context"
	"database/sql"
)

type Service struct{ db *sql.DB }

func NewService(editorialDB *sql.DB) *Service { return &Service{db: editorialDB} }

func (s *Service) CreateDraft(ctx context.Context, input map[string]TranslationPatch) (ArticleState, error) {
	patch, err := normalizePatch(input)
	if err != nil {
		return ArticleState{}, err
	}
	id, err := newID()
	if err != nil {
		return ArticleState{}, err
	}
	tx, err := s.begin(ctx, false)
	if err != nil {
		return ArticleState{}, err
	}
	defer rollback(tx)
	state := ArticleState{ID: id, Version: 1}
	if _, err = tx.ExecContext(ctx, "INSERT INTO public.articles(id) VALUES($1)", id); err != nil {
		return ArticleState{}, databaseError(err)
	}
	if _, err = savePatch(ctx, tx, id, patch); err != nil {
		return ArticleState{}, err
	}
	return commitState(tx, state)
}

func (s *Service) SaveDraft(ctx context.Context, id string, expectedVersion int64, input map[string]TranslationPatch) (ArticleState, error) {
	patch, err := normalizePatch(input)
	if err != nil {
		return ArticleState{}, err
	}
	tx, state, err := s.beginArticle(ctx, id, expectedVersion)
	if err != nil {
		return ArticleState{}, err
	}
	defer rollback(tx)
	changed, err := savePatch(ctx, tx, id, patch)
	if err != nil {
		return ArticleState{}, err
	}
	if changed {
		if err = advanceVersion(ctx, tx, &state); err != nil {
			return ArticleState{}, err
		}
	}
	return commitState(tx, state)
}

func (s *Service) MarkTranslationReady(ctx context.Context, id, languageCode string, expectedVersion int64) (ArticleState, error) {
	code, err := normalizeLanguage(languageCode)
	if err != nil {
		return ArticleState{}, err
	}
	tx, state, err := s.beginArticle(ctx, id, expectedVersion)
	if err != nil {
		return ArticleState{}, err
	}
	defer rollback(tx)
	enabled, err := lockLanguages(ctx, tx, []string{code})
	if err != nil {
		return ArticleState{}, err
	}
	if !enabled[code] {
		return ArticleState{}, problem(Validation, "language_unavailable")
	}
	result, err := tx.ExecContext(ctx, "UPDATE public.article_draft_translations SET is_ready=true WHERE article_id=$1 AND language_code=$2 AND NOT is_ready", id, code)
	if err != nil {
		return ArticleState{}, databaseError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ArticleState{}, databaseError(err)
	}
	if count != 0 {
		if err = advanceVersion(ctx, tx, &state); err != nil {
			return ArticleState{}, err
		}
	} else if err = requireDraft(ctx, tx, id, code); err != nil {
		return ArticleState{}, err
	}
	return commitState(tx, state)
}

func (s *Service) Publish(ctx context.Context, id string, expectedVersion int64) (ArticleState, error) {
	tx, state, err := s.beginArticle(ctx, id, expectedVersion)
	if err != nil {
		return ArticleState{}, err
	}
	defer rollback(tx)
	ready, err := readyTranslations(ctx, tx, id)
	if err != nil {
		return ArticleState{}, err
	}
	current, err := currentTranslations(ctx, tx, state.PublishedRevisionID)
	if err != nil {
		return ArticleState{}, err
	}
	snapshot, err := publicationSnapshot(ctx, tx, current, ready)
	if err != nil {
		return ArticleState{}, err
	}
	revision, err := newID()
	if err != nil {
		return ArticleState{}, err
	}
	if err = storeSnapshot(ctx, tx, state, revision, snapshot, ready); err != nil {
		return ArticleState{}, err
	}
	state.PublishedRevisionID = &revision
	if err = advanceVersion(ctx, tx, &state); err != nil {
		return ArticleState{}, err
	}
	return commitState(tx, state)
}

func (s *Service) UnpublishArticle(ctx context.Context, id string, expectedVersion int64) (ArticleState, error) {
	tx, state, err := s.beginArticle(ctx, id, expectedVersion)
	if err != nil {
		return ArticleState{}, err
	}
	defer rollback(tx)
	result, err := tx.ExecContext(ctx, "UPDATE public.article_draft_translations SET is_ready=false WHERE article_id=$1 AND is_ready", id)
	if err != nil {
		return ArticleState{}, databaseError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ArticleState{}, databaseError(err)
	}
	if state.PublishedRevisionID != nil || count != 0 {
		state.PublishedRevisionID = nil
		if err = advanceVersion(ctx, tx, &state); err != nil {
			return ArticleState{}, err
		}
	}
	return commitState(tx, state)
}

func (s *Service) begin(ctx context.Context, readOnly bool) (*sql.Tx, error) {
	level := sql.LevelReadCommitted
	if readOnly {
		level = sql.LevelRepeatableRead
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly, Isolation: level})
	if err != nil {
		return nil, databaseError(err)
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, "SELECT pg_catalog.pg_has_role(current_user,'sp_publisher','USAGE')").Scan(&allowed)
	if err != nil || !allowed {
		rollback(tx)
		if err != nil {
			return nil, databaseError(err)
		}
		return nil, problem(Forbidden, "editorial_access_denied")
	}
	return tx, nil
}

func (s *Service) beginArticle(ctx context.Context, id string, version int64) (*sql.Tx, ArticleState, error) {
	if err := validateID(id); err != nil {
		return nil, ArticleState{}, err
	}
	if version < 1 {
		return nil, ArticleState{}, problem(Validation, "invalid_version")
	}
	tx, err := s.begin(ctx, false)
	if err != nil {
		return nil, ArticleState{}, err
	}
	state, err := lockArticle(ctx, tx, id)
	if err == nil && state.Version != version {
		err = problem(Conflict, "version_conflict")
	}
	if err != nil {
		rollback(tx)
		return nil, ArticleState{}, err
	}
	return tx, state, nil
}

func commitState(tx *sql.Tx, state ArticleState) (ArticleState, error) {
	if err := tx.Commit(); err != nil {
		return ArticleState{}, databaseError(err)
	}
	return state, nil
}

func rollback(tx *sql.Tx) {
	// Любой отказ до успешного Commit откатывает всю команду; результат после Commit уже закрыт.
	_ = tx.Rollback()
}
