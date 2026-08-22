package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/vincommerce/backend/internal/domain"
)

var logger = slog.Default()

// SetLogger overrides the package-level logger.
func SetLogger(l *slog.Logger) { logger = l }

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var de *domain.Error
	if errors.As(err, &de) {
		status := statusFor(de.Kind)
		if status >= 500 {
			logger.Error("request failed", "error", err, "path", r.URL.Path)
		}
		body := errorBody{}
		body.Error.Code = de.Code
		body.Error.Message = de.Message
		writeJSON(w, status, body)
		return
	}
	logger.Error("unhandled error", "error", err, "path", r.URL.Path)
	writeJSON(w, http.StatusInternalServerError, errorBody{})
}

func statusFor(kind domain.ErrorKind) int {
	switch kind {
	case domain.KindInvalid:
		return http.StatusBadRequest
	case domain.KindUnauthenticated:
		return http.StatusUnauthorized
	case domain.KindForbidden:
		return http.StatusForbidden
	case domain.KindNotFound:
		return http.StatusNotFound
	case domain.KindConflict:
		return http.StatusConflict
	case domain.KindRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}
