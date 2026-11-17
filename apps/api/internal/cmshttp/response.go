package cmshttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"stepolonia/apps/api/internal/cms"
)

const (
	contentTypeHeader = "Content-Type"
	jsonMediaType     = "application/json"
)

const (
	authenticationCode = "authentication_required"
	originCode         = "origin_denied"
	validationCode     = "validation_failed"
	csrfCode           = "csrf_failed"
	notFoundCode       = "not_found"
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
	Status int    `json:"status"`
	Code   string `json:"code"`
	Title  string `json:"title"`
}

func respond(w http.ResponseWriter, value any) {
	w.Header().Set(contentTypeHeader, jsonMediaType)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set(contentTypeHeader, "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(authProblem{Status: status, Code: code, Title: http.StatusText(status)})
}
func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "security_unavailable"
	switch {
	case errors.Is(err, cms.ErrInvalidCredentials):
		status, code = http.StatusUnauthorized, "invalid_credentials"
	case errors.Is(err, cms.ErrAuthenticationRequired):
		status, code = http.StatusUnauthorized, authenticationCode
	case errors.Is(err, cms.ErrCSRF):
		status, code = http.StatusForbidden, csrfCode
	case errors.Is(err, cms.ErrPermissionDenied):
		status, code = http.StatusForbidden, "permission_denied"
	case errors.Is(err, cms.ErrStepUpRequired):
		status, code = http.StatusForbidden, "step_up_required"
	case errors.Is(err, cms.ErrVersionConflict):
		status, code = http.StatusConflict, "version_conflict"
	case errors.Is(err, cms.ErrInvalidTransition):
		status, code = http.StatusConflict, "invalid_transition"
	case errors.Is(err, cms.ErrNotFound):
		status, code = http.StatusNotFound, notFoundCode
	case errors.Is(err, cms.ErrValidation):
		status, code = http.StatusBadRequest, validationCode
	case errors.Is(err, cms.ErrRateLimited):
		status, code = http.StatusTooManyRequests, "rate_limited"
		w.Header().Set("Retry-After", "900")
	}
	problem(w, status, code)
}
