package cmsintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"stepolonia/apps/api/internal/cms"
)

const assigneeBrowserSessionMode = "session"

func TestAuthContentAssigneesBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	t.Run("session and authority", func(t *testing.T) {
		runContentAssigneesBrowser(t, assigneeBrowserSessionMode)
	})
	t.Run("scope and replacement", func(t *testing.T) {
		runContentAssigneesBrowser(t, "replacement")
	})
}

func runContentAssigneesBrowser(t *testing.T, mode string) {
	t.Helper()
	x := newAssigneeFixture(t)
	f := x.http.state
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var inactiveID, publicationID string
	if mode == assigneeBrowserSessionMode {
		expireSessionStepUp(t, f.db, x.actor.Token)
	} else {
		inactiveID = lifecycleEnrolled(t, f, x.admin, "inactive-assignee@example.test").user.ID
		publicationID = seedAssigneePublication(t, f.db)
	}
	_, err := f.db.Exec(`UPDATE public.mfa_totp SET last_counter=last_counter-1 WHERE user_id IN ($1,$2)`,
		x.manager.user.ID, x.recipient.user.ID)
	requireNoError(t, err)
	command := exec.CommandContext(ctx, nodeExecutable, "content_assignees_browser.cjs")
	command.Env = append(os.Environ(),
		"ASSIGN_ORIGIN="+x.http.server.URL, "ASSIGN_MODE="+mode,
		"ASSIGN_PASSWORD="+bootstrapTestPassphrase,
		"ASSIGN_MANAGER_LOGIN="+x.manager.user.Login,
		"ASSIGN_MANAGER_CODE="+bootstrapCode(t, f.db, x.manager.secret),
		"ASSIGN_RECIPIENT_LOGIN="+x.recipient.user.Login,
		"ASSIGN_RECIPIENT_CODE="+bootstrapCode(t, f.db, x.recipient.secret),
		"ASSIGN_STALE_TOKEN="+x.actor.Token, "ASSIGN_STALE_CSRF="+x.actor.CSRF,
		"ASSIGN_RECIPIENT="+x.recipient.user.ID, "ASSIGN_MANAGER="+x.manager.user.ID,
		"ASSIGN_INACTIVE="+inactiveID, "ASSIGN_UNKNOWN="+assigneeMissingID,
		"ASSIGN_ITEM="+assigneeItem, "ASSIGN_TYPE="+assigneeTypeKey,
		"ASSIGN_OTHER_ITEM="+assigneeOtherItem, "ASSIGN_OTHER_TYPE="+assigneeOtherKey,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Chromium content assignment: %v: %s", err, output)
	}
	if mode == assigneeBrowserSessionMode {
		assertItemAssignees(t, f.db, assigneeItem, 1)
		return
	}
	var requestIDs [2]string
	requireNoError(t, json.Unmarshal(output, &requestIDs))
	assertItemAssignees(t, f.db, assigneeItem, 3)
	assertItemAssignees(t, f.db, assigneeOtherItem, 1)
	var currentPublication string
	requireNoError(t, f.db.QueryRow(`SELECT published_id::text FROM public.content_items WHERE id=$1`, assigneeItem).Scan(&currentPublication))
	if currentPublication != publicationID {
		t.Fatal("assignee replacement changed the publication pointer")
	}
	var audited bool
	requireNoError(t, f.db.QueryRow(`SELECT count(*)=2 AND bool_and(actor_id=$1 AND request_id IN ($3,$4))
 FROM public.security_audit WHERE target_id=$2 AND operation='content.assignees_replaced' AND result='success'`,
		x.manager.user.ID, assigneeItem, requestIDs[0], requestIDs[1]).Scan(&audited))
	if !audited {
		t.Fatal("assignment success audit does not correlate with the browser requests")
	}
}

func seedAssigneePublication(t *testing.T, db *sql.DB) string {
	t.Helper()
	const publicationID = "a3000000-0000-4000-8000-000000000006"
	requireNoError(t, cms.WithWriteMutex(context.Background(), db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.languages(code,name,native_name,direction,is_enabled)
 VALUES('en','English','English','ltr',true)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_publications(id,item_id,type_id,schema_no,revision_no,shared_seq)
 VALUES($1,$2,$3,1,1,1)`, publicationID, assigneeItem, assigneeTypeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_routes(namespace,locale_code,slug,type_id,item_id)
 VALUES('assignment-articles','en','published-item',$1,$2)`, assigneeTypeID, assigneeItem); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.content_publication_locales(publication_id,item_id,locale_code,locale_generation,slug)
 SELECT $1,$2,'en',generation,'published-item' FROM public.languages WHERE code='en'`, publicationID, assigneeItem); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.content_publications SET sealed=true WHERE id=$1`, publicationID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE public.content_items SET published_id=$1 WHERE id=$2`, publicationID, assigneeItem)
		return err
	}))
	return publicationID
}
