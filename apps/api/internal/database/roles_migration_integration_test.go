package database

import (
	"context"
	"testing"
)

func TestAuthRolesMigrationBoundaryIntegration(t *testing.T) {
	db := newAuthHTTPFixture(t).state.db
	ctx := context.Background()
	requireNoError(t, CheckSchema(ctx, db))
	p, err := Provider(db)
	requireNoError(t, err)
	results, err := p.Up(ctx)
	requireNoError(t, err)
	if len(results) != 0 {
		t.Fatal("repeat Up changed applied history")
	}
	_, err = p.DownTo(ctx, 5)
	requireNoError(t, err)
	var removed bool
	requireNoError(t, db.QueryRow(`SELECT NOT has_column_privilege('sp_security','public.content_types','id','SELECT') AND NOT has_column_privilege('sp_security','public.content_fields','type_id','SELECT') AND NOT has_column_privilege('sp_security','public.content_fields','field_id','SELECT')`).Scan(&removed))
	if !removed {
		t.Fatal("RBAC Down retained Registry identity privileges")
	}
	_, err = p.Up(ctx)
	requireNoError(t, err)
	requireNoError(t, CheckSchema(ctx, db))
	_, err = p.DownTo(ctx, 0)
	requireNoError(t, err)
	_, err = p.Up(ctx)
	requireNoError(t, err)
	requireNoError(t, CheckSchema(ctx, db))
}
