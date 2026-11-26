package adminhttp

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"time"

	"stepolonia/apps/api/internal/identity"
)

// OnboardingService supplies authorized user commands and restricted self-enrollment.
// It is separate from AuthService so authentication-only consumers remain valid.
type OnboardingService interface {
	CreateUser(context.Context, identity.AuthRequest, string) (identity.User, error)
	IssueOnboardingToken(context.Context, identity.AuthRequest, string, int64) (identity.OnboardingToken, error)
	StartEnrollment(context.Context, identity.AuthRequest, string, string) (identity.Enrollment, error)
	CompleteEnrollment(context.Context, identity.AuthRequest, string) ([]string, error)
}

var _ OnboardingService = (*identity.AuthService)(nil)

const (
	usersPath              = "/api/v1/admin/users"
	onboardingTokenPath    = usersPath + "/{id}/onboarding-token"
	enrollmentPath         = "/enrollment"
	enrollmentCompletePath = "/enrollment/complete"
)

const (
	createUserOperation      = "usersCreate"
	issueOnboardingOperation = "usersIssueOnboardingToken"
	expectedVersionField     = "expected_version"
)

var userRoutes = map[string][]route{
	userPath:         {getUserRoute},
	activateUserPath: {activateUserRoute},
	disableUserPath:  {disableUserRoute},
	userRolesPath:    {readUserRolesRoute, replaceUserRolesRoute},
	usersPath: {listUsersRoute, {kind: fullKind, fields: requestFields(createUserInput{}), method: http.MethodPost, operationID: createUserOperation, summary: "Create a pending account", response: pendingUserSchema,
		description: "Requires an active full session, effective users.create permission and strong authentication no older than five minutes. Creates a pending account without credentials, roles or sessions. The actor and permissions come exclusively from the verified server session. The successful write and audit commit atomically."}},
	onboardingTokenPath: {{kind: fullKind, fields: expectedVersionField, method: http.MethodPost, operationID: issueOnboardingOperation, summary: "Issue or replace a pending user's onboarding token", response: "OnboardingToken",
		description: "Requires an active full session, effective users.create permission and strong authentication no older than five minutes. The target must still be pending and expected_version must match its current version under the write mutex. Atomically revokes the previous onboarding token and restricted enrollment and returns a new 24-hour one-time token only after audit commits. Deliver it over a verified external channel; never place it in a URL or log."}},
}

func resolveRoute(path, method string, draftEditorEnabled bool) (route, routeTarget, bool) {
	if strings.HasPrefix(path, authPrefix+"/") {
		key := strings.TrimPrefix(path, authPrefix)
		rt, ok := routes[key]
		return rt, routeTarget{path: key}, ok
	}
	if target, ok := contentAssigneesTarget(path); ok {
		return routeForMethod(method, []route{readContentAssigneesRoute, replaceContentAssigneesRoute}), target, true
	}
	if draftEditorEnabled {
		rt, target, ok := resolveContentEditorRoute(path, method)
		if ok {
			return rt, target, true
		}
	}
	key, id, candidates := staticAdminRoutes(path)
	if len(candidates) == 0 {
		return route{}, routeTarget{}, false
	}
	return routeForMethod(method, candidates), routeTarget{path: key, id: id}, true
}

func resolveContentEditorRoute(path, method string) (route, routeTarget, bool) {
	if target, ok := createDraftTarget(path); ok {
		return routeForMethod(method, collectionDraftRoutes), target, true
	}
	if target, ok := contentDraftTarget(path); ok {
		return routeForMethod(method, itemDraftRoutes), target, true
	}
	if target, rt, ok := draftCandidateTarget(path); ok {
		return rt, target, true
	}
	if target, rt, ok := registryTarget(path, method); ok {
		return rt, target, true
	}
	if target, rt, ok := contentMetadataTarget(path); ok {
		return rt, target, true
	}
	return route{}, routeTarget{}, false
}

func routeForMethod(method string, candidates []route) route {
	for _, rt := range candidates {
		if rt.method == method {
			return rt
		}
	}
	return candidates[0]
}

func staticAdminRoutes(path string) (string, string, []route) {
	if candidates := adminUIRoutes[path]; candidates != nil {
		return path, "", candidates
	}
	if path == roleGrantOptionsPath {
		return path, "", []route{readRoleGrantOptionsRoute}
	}
	if key, id := accountTarget(path); key != "" {
		return key, id, []route{accountRoutes[key]}
	}
	switch path {
	case sessionPolicyPath:
		return path, "", sessionPolicyRoutes
	case ratePolicyPath:
		return path, "", ratePolicyRoutes
	}
	if candidates := settingsRoutes[path]; candidates != nil {
		return path, "", candidates
	}
	key, id := userRoutePath(path)
	if key == targetSessionsPath {
		return key, id, []route{accountRoutes[key]}
	}
	if candidates := userRoutes[key]; candidates != nil {
		return key, id, candidates
	}
	key, id = roleRoutePath(path)
	return key, id, roleRoutes[key]
}

func userRoutePath(path string) (string, string) {
	if path == usersPath {
		return usersPath, ""
	}
	if !strings.HasPrefix(path, usersPath+"/") {
		return "", ""
	}
	segments := strings.Split(strings.TrimPrefix(path, usersPath+"/"), "/")
	if segments[0] == "" {
		return "", ""
	}
	if len(segments) == 1 {
		return userPath, segments[0]
	}
	if len(segments) == 2 {
		switch segments[1] {
		case "onboarding-token":
			return onboardingTokenPath, segments[0]
		case "activate":
			return activateUserPath, segments[0]
		case "disable":
			return disableUserPath, segments[0]
		case "roles":
			return userRolesPath, segments[0]
		case "sessions":
			return targetSessionsPath, segments[0]
		}
	}
	return "", ""
}

type pendingUserResponse struct {
	ID      string `json:"id" format:"uuid" doc:"Stable pending account UUID."`
	Login   string `json:"login" doc:"Normalized account login identifier."`
	State   string `json:"state" enum:"pending" doc:"Pending until the account completes its own enrollment."`
	Version int64  `json:"version" minimum:"1" doc:"Current optimistic concurrency version for this account."`
}

type onboardingTokenResponse struct {
	Token     string              `json:"token" doc:"One-time random onboarding token. Only its digest is persisted."`
	ExpiresAt time.Time           `json:"expires_at" doc:"Server expiry, 24 hours after issuance."`
	User      pendingUserResponse `json:"user"`
}

func userResponse(user identity.User) pendingUserResponse {
	return pendingUserResponse{ID: user.ID, Login: user.Login, State: user.State, Version: user.Version}
}

func (h *handler) executeUser(w http.ResponseWriter, r *http.Request, path, userID string, req identity.AuthRequest, input credentials) {
	service, ok := h.service.(OnboardingService)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	if path == usersPath {
		user, err := service.CreateUser(r.Context(), req, input.Login)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, userResponse(user))
		return
	}
	out, err := service.IssueOnboardingToken(r.Context(), req, userID, input.ExpectedVersion)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, onboardingTokenResponse{Token: out.Token, ExpiresAt: out.ExpiresAt, User: userResponse(out.User)})
}

func (h *handler) executeEnrollment(w http.ResponseWriter, r *http.Request, path string, req identity.AuthRequest, input credentials) {
	service, ok := h.service.(OnboardingService)
	if !ok {
		writeError(w, identity.ErrSecurityUnavailable)
		return
	}
	if path == enrollmentPath {
		out, err := service.StartEnrollment(r.Context(), req, input.Token, input.Password)
		if err != nil {
			writeError(w, err)
			return
		}
		setSession(w, out.SessionTokens)
		respond(w, recoveryEnrollmentResponse{CSRFToken: out.CSRF, Secret: out.Secret})
		return
	}
	codes, err := service.CompleteEnrollment(r.Context(), req, input.Code)
	if err != nil {
		writeError(w, err)
		return
	}
	clearCookies(w)
	respond(w, recoveryCodesResponse{RecoveryCodes: codes})
}

type createUserInput struct {
	Login string `json:"login"`
}

func requestFields(input any) string {
	typ := reflect.TypeOf(input)
	fields := make([]string, typ.NumField())
	for i := range fields {
		fields[i] = strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
	}
	return strings.Join(fields, ",")
}

func isOnboardingRoute(path string) bool {
	switch path {
	case usersPath, onboardingTokenPath, enrollmentPath, enrollmentCompletePath:
		return true
	default:
		return false
	}
}

func routeMethods(path string) string {
	switch path {
	case sessionPolicyPath, contentAssigneesPath:
		return "GET, PUT"
	case roleGrantOptionsPath, draftMetadataPath, registryRenderersPath, adminEntryPath, adminCatalogPath:
		return http.MethodGet
	case contentCollectionsPath, ratePolicyPath:
		return "GET, POST"
	case registryCollectionPath:
		return "GET, PATCH"
	}
	if rt, ok := accountRoutes[path]; ok {
		return rt.method
	}
	if rt, ok := draftCandidateRoutes[path]; ok {
		return rt.method
	}
	if path == createDraftPath {
		return listDraftsRoute.method + ", " + createDraftRoute.method
	}
	if path == contentDraftPath {
		return readDraftRoute.method + ", " + saveDraftRoute.method
	}
	if rt, ok := routes[path]; ok {
		return rt.method
	}
	methods := make([]string, 0, len(userRoutes[path]))
	candidates := userRoutes[path]
	if routes := settingsRoutes[path]; routes != nil {
		candidates = routes
	}
	if isRoleRoute(path) {
		candidates = roleRoutes[path]
	}
	for _, rt := range candidates {
		methods = append(methods, rt.method)
	}
	return strings.Join(methods, ", ")
}
