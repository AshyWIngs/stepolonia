package cmsintegration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestAuthUserRolesBrowserIntegration(t *testing.T) {
	requireAuthBrowser(t)
	f := newRestrictedAuthHTTPFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin := authFull(t, f.state)
	recipient := lifecycleEnrolled(t, f.state, admin, "membership-browser@example.test")
	user, err := f.state.service.ActivateUser(ctx, authReq(admin), recipient.user.ID, recipient.user.Version)
	requireNoError(t, err)
	oldRole := createAssignedTestRole(t, f.state, "Browser role reader", lifecycleHTTPActiveState, user.ID)
	userReader := createMembershipTestRole(t, f.state, "Browser user reader", lifecycleHTTPActiveState)
	inactiveReader := createMembershipTestRole(t, f.state, "Inactive role reader", lifecycleHTTPDisabledState)
	const readRoles = "roles.read"
	addTestInstallationGrant(t, f.state, oldRole, readRoles)
	addTestInstallationGrant(t, f.state, inactiveReader, readRoles)
	addTestInstallationGrant(t, f.state, userReader, userReaderGrant().Operation)
	_, err = f.state.db.Exec(`UPDATE public.mfa_totp SET last_counter=last_counter-1 WHERE user_id IN ($1,$2)`, f.state.user, user.ID)
	requireNoError(t, err)

	command := exec.CommandContext(ctx, nodeExecutable, "user_roles_browser.cjs")
	command.Env = append(os.Environ(),
		"MEMBERSHIP_ORIGIN="+f.server.URL, "MEMBERSHIP_PASSWORD="+bootstrapTestPassphrase,
		"MEMBERSHIP_ADMIN_LOGIN="+bootstrapTestLogin, "MEMBERSHIP_ADMIN_CODE="+bootstrapCode(t, f.state.db, f.state.secret),
		"MEMBERSHIP_RECIPIENT_LOGIN="+user.Login, "MEMBERSHIP_RECIPIENT_CODE="+bootstrapCode(t, f.state.db, recipient.secret),
		"MEMBERSHIP_RECIPIENT_ID="+user.ID, "MEMBERSHIP_VERSION="+strconv.FormatInt(user.Version, 10),
		"MEMBERSHIP_OLD_ROLE="+oldRole, "MEMBERSHIP_ACTIVE_ROLE="+userReader, "MEMBERSHIP_INACTIVE_ROLE="+inactiveReader,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real membership Chromium scenario: %v: %s", err, output)
	}
	var requestIDs [2]string
	requireNoError(t, json.Unmarshal(output, &requestIDs))
	assertMembershipState(t, f.state, user, user.Version+2, nil)
	var audited bool
	requireNoError(t, f.state.db.QueryRow(`
 SELECT count(*)=2 AND bool_and(actor_id=$1 AND request_id IN ($3,$4))
 FROM public.security_audit WHERE target_id=$2 AND operation='users.roles_replaced' AND result='success'`,
		f.state.user, user.ID, requestIDs[0], requestIDs[1]).Scan(&audited))
	if !audited {
		t.Fatal("membership replacement lost success audit correlation")
	}
}
