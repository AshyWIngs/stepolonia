package adminintegration

import (
	"context"
	"stepolonia/apps/api/internal/identity"
	"testing"
)

func TestAuthLifecycleCapacityIntegration(t *testing.T) {
	cases := []struct{ name, query string }{
		{"account capacity", `INSERT INTO public.users(id,login_key) SELECT gen_random_uuid(),'cap-'||i::text FROM generate_series(1,100) i`},
		{"role capacity", `INSERT INTO public.roles(id,name) SELECT gen_random_uuid(),'cap-role-'||i::text FROM generate_series(1,30) i`},
		{"memberships", `WITH r AS (INSERT INTO public.roles(id,name) SELECT gen_random_uuid(),'cap-member-'||i::text FROM generate_series(1,17) i RETURNING id) INSERT INTO public.user_roles(user_id,role_id) SELECT $1,id FROM r`},
		{"grants", `WITH types AS (INSERT INTO public.content_types(id,key,route_namespace) SELECT gen_random_uuid(),'cap_type_'||i::text,'cap-type-'||i::text FROM generate_series(1,65) i RETURNING id),schemas AS (INSERT INTO public.content_schemas(type_id,schema_no,sealed) SELECT id,1,true FROM types) INSERT INTO public.role_grants(id,role_id,operation,scope,type_id,object_scope) SELECT gen_random_uuid(),(SELECT role_id FROM public.user_roles WHERE user_id=$1 LIMIT 1),'content.items.read','content',id,'any' FROM types`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestrictedAuthHTTPFixture(t).state
			full := authFull(t, f)
			var err error
			if tc.name == "account capacity" || tc.name == "role capacity" {
				_, err = f.db.Exec(tc.query)
			} else {
				_, err = f.db.Exec(tc.query, f.user)
			}
			requireNoError(t, err)
			_, err = f.service.ListUsers(context.Background(), authReq(full), identity.UserPage{})
			onboardingError(t, err, identity.ErrSecurityUnavailable)
			var role string
			requireNoError(t, f.db.QueryRow(`SELECT role_id::text FROM public.user_roles WHERE user_id=$1 LIMIT 1`, f.user).Scan(&role))
			_, err = f.service.DisableRole(context.Background(), authReq(full), role, 1)
			onboardingError(t, err, identity.ErrSecurityUnavailable)
			_, err = f.service.DisableUser(context.Background(), authReq(full), f.user, 2)
			onboardingError(t, err, identity.ErrSecurityUnavailable)
		})
	}
}

func TestAuthLifecycleOperationalFactorsIntegration(t *testing.T) {
	cases := []struct{ name, query string }{
		{"missing totp", `DELETE FROM public.mfa_totp WHERE user_id=$1`},
		{"unconfirmed totp", `UPDATE public.mfa_totp SET confirmed_at=NULL WHERE user_id=$1`},
		{"no recovery", `UPDATE public.recovery_codes SET consumed_at=clock_timestamp() WHERE user_id=$1`},
		{"unsupported password", `UPDATE public.users SET password_hash='$argon2id$unsupported-login-password-hash-0000000' WHERE id=$1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestrictedAuthHTTPFixture(t).state
			full := authFull(t, f)
			account := lifecycleEnrolled(t, f, full, lifecycleLogin)
			_, err := f.db.Exec(tc.query, account.user.ID)
			requireNoError(t, err)
			_, err = f.service.ActivateUser(context.Background(), authReq(full), account.user.ID, account.user.Version)
			onboardingError(t, err, identity.ErrInvalidTransition)
		})
	}
}

func TestAuthLifecyclePaginationBoundsIntegration(t *testing.T) {
	f := newRestrictedAuthHTTPFixture(t).state
	full := authFull(t, f)
	for _, page := range []identity.UserPage{{Limit: -1}, {Limit: 101}, {Offset: -1}, {Offset: 101}} {
		_, err := f.service.ListUsers(context.Background(), authReq(full), page)
		onboardingError(t, err, identity.ErrValidation)
	}
	page, err := f.service.ListUsers(context.Background(), authReq(full), identity.UserPage{Offset: 100})
	requireNoError(t, err)
	if page.Items == nil || len(page.Items) != 0 || page.NextOffset != nil {
		t.Fatal("empty bounded page")
	}
}
