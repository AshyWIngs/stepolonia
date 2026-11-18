package cmshttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"stepolonia/apps/api/internal/cms"
)

const (
	contentTypeHeader = "Content-Type"
	requestIDHeader   = "X-Request-ID"
	jsonMediaType     = "application/json"
)

const (
	authenticationCode      = "authentication_required"
	originCode              = "origin_denied"
	validationCode          = "validation_failed"
	csrfCode                = "csrf_failed"
	notFoundCode            = "not_found"
	permissionCode          = "permission_denied"
	methodNotAllowedCode    = "method_not_allowed"
	bodyTooLargeCode        = "body_too_large"
	securityUnavailableCode = "security_unavailable"
	stepUpRequiredCode      = "step_up_required"
	lastAdminRequiredCode   = "last_admin_required"
	versionConflictCode     = "version_conflict"
	roleInUseCode           = "role_in_use"
	invalidTransitionCode   = "invalid_transition"
	rateLimitedCode         = "rate_limited"
)

type csrfResponse struct {
	CSRFToken string `json:"csrf_token"`
}
type recoveryEnrollmentResponse struct {
	CSRFToken string `json:"csrf_token"`
	Secret    string `json:"secret"`
}
type recoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}
type authProblem struct {
	Status int    `json:"status" doc:"HTTP response status." example:"401"`
	Code   string `json:"code" doc:"Stable machine-readable failure reason; each status response constrains the supported codes." example:"authentication_required"`
	Title  string `json:"title" doc:"Standard HTTP status phrase." example:"Unauthorized"`
}

func respond(w http.ResponseWriter, value any) {
	respondStatus(w, http.StatusOK, value)
}

func respondStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set(contentTypeHeader, jsonMediaType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set(contentTypeHeader, "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(authProblem{Status: status, Code: code, Title: http.StatusText(status)})
}
func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, securityUnavailableCode
	switch {
	case errors.Is(err, cms.ErrInvalidCredentials):
		status, code = http.StatusUnauthorized, "invalid_credentials"
	case errors.Is(err, cms.ErrAuthenticationRequired):
		status, code = http.StatusUnauthorized, authenticationCode
	case errors.Is(err, cms.ErrCSRF):
		status, code = http.StatusForbidden, csrfCode
	case errors.Is(err, cms.ErrPermissionDenied):
		status, code = http.StatusForbidden, permissionCode
	case errors.Is(err, cms.ErrStepUpRequired):
		status, code = http.StatusForbidden, stepUpRequiredCode
	case errors.Is(err, cms.ErrLastAdminRequired):
		status, code = http.StatusConflict, lastAdminRequiredCode
	case errors.Is(err, cms.ErrVersionConflict):
		status, code = http.StatusConflict, versionConflictCode
	case errors.Is(err, cms.ErrRoleInUse):
		status, code = http.StatusConflict, roleInUseCode
	case errors.Is(err, cms.ErrInvalidTransition):
		status, code = http.StatusConflict, invalidTransitionCode
	case errors.Is(err, cms.ErrNotFound):
		status, code = http.StatusNotFound, notFoundCode
	case errors.Is(err, cms.ErrValidation):
		status, code = http.StatusBadRequest, validationCode
	case errors.Is(err, cms.ErrRateLimited):
		status, code = http.StatusTooManyRequests, rateLimitedCode
		w.Header().Set("Retry-After", "900")
	}
	problem(w, status, code)
}
